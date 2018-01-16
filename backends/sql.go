package backends

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/ghetzel/go-stockutil/maputil"
	"github.com/ghetzel/go-stockutil/sliceutil"
	"github.com/ghetzel/go-stockutil/typeutil"
	"github.com/ghetzel/pivot/dal"
	"github.com/ghetzel/pivot/filter"
	"github.com/ghetzel/pivot/filter/generators"
)

var objectFieldHintLength = 131071

type sqlTableDetails struct {
	Index        int
	Name         string
	Type         string
	TypeLength   int
	Precision    int
	NativeType   string
	PrimaryKey   bool
	KeyField     bool
	Nullable     bool
	Unique       bool
	DefaultValue string
}

type additionalIndexer struct {
	ForCollection string
	Indexer       Indexer
}

type sqlTableDetailsFunc func(datasetName string, collectionName string) (*dal.Collection, error)

type SqlBackend struct {
	Backend
	Indexer
	Aggregator
	conn                        *dal.ConnectionString
	db                          *sql.DB
	indexer                     Indexer
	aggregator                  map[string]Aggregator
	queryGenTypeMapping         generators.SqlTypeMapping
	queryGenPlaceholderArgument string
	queryGenPlaceholderFormat   string
	queryGenTableFormat         string
	queryGenFieldFormat         string
	queryGenNestedFieldFormat   string
	queryGenNormalizerFormat    string
	listAllTablesQuery          string
	createPrimaryKeyIntFormat   string
	createPrimaryKeyStrFormat   string
	showTableDetailQuery        string
	refreshCollectionFunc       sqlTableDetailsFunc
	dropTableQuery              string
	registeredCollections       sync.Map
	knownCollections            map[string]bool
}

func NewSqlBackend(connection dal.ConnectionString) Backend {
	backend := &SqlBackend{
		conn:                      &connection,
		queryGenTypeMapping:       generators.DefaultSqlTypeMapping,
		queryGenPlaceholderFormat: `?`,
		dropTableQuery:            `DROP TABLE %s`,
		aggregator:                make(map[string]Aggregator),
		knownCollections:          make(map[string]bool),
	}

	backend.indexer = backend
	return backend
}

func (self *SqlBackend) GetConnectionString() *dal.ConnectionString {
	return self.conn
}

func (self *SqlBackend) RegisterCollection(collection *dal.Collection) {
	if collection != nil {
		self.registeredCollections.Store(collection.Name, collection)
		log.Debugf("[%T] register collection %v", self, collection.Name)
	}
}

func (self *SqlBackend) SetIndexer(indexConnString dal.ConnectionString) error {
	if indexer, err := MakeIndexer(indexConnString); err == nil {
		self.indexer = indexer
		return nil
	} else {
		return err
	}
}

func (self *SqlBackend) Initialize() error {
	backend := self.conn.Backend()
	internalBackend := backend

	var name string
	var dsn string
	var err error

	// setup driver-specific settings
	switch backend {
	case `sqlite`:
		name, dsn, err = self.initializeSqlite()
	case `mysql`:
		name, dsn, err = self.initializeMysql()
	case `postgres`:
		name, dsn, err = self.initializePostgres()
	default:
		return fmt.Errorf("Unsupported backend %q", backend)
	}

	if err != nil {
		return err
	} else if name != `` {
		internalBackend = name
	}

	// setup the database driver for use
	if db, err := sql.Open(internalBackend, dsn); err == nil {
		self.db = db
	} else {
		return err
	}

	// actually verify database connectivity at this time
	if err := self.db.Ping(); err != nil {
		return err
	}

	// refresh schema cache
	if err := self.refreshAllCollections(); err != nil {
		return err
	}

	if err := self.indexer.IndexInitialize(self); err != nil {
		return err
	}

	// setup aggregators (currently this is just the SQL implementation)
	self.aggregator[``] = self

	return nil
}

func (self *SqlBackend) Insert(name string, recordset *dal.RecordSet) error {
	if collection, err := self.getCollectionFromCache(name); err == nil {
		if tx, err := self.db.Begin(); err == nil {
			switch self.conn.Backend() {
			case `mysql`:
				// disable zero-means-use-autoincrement for inserts in MySQL
				if _, err := tx.Exec(`SET sql_mode='NO_AUTO_VALUE_ON_ZERO'`); err != nil {
					defer tx.Rollback()
					return err
				}
			}

			// for each record being inserted...
			for _, record := range recordset.Records {
				if r, err := collection.MakeRecord(record); err == nil {
					record = r
				} else {
					return err
				}

				// setup query generator
				queryGen := self.makeQueryGen(collection)
				queryGen.Type = generators.SqlInsertStatement

				// add record data to query input
				for k, v := range record.Fields {
					// convert incoming values to their destination field types
					queryGen.InputData[k] = collection.ConvertValue(k, v)
				}

				// set the primary key
				if !typeutil.IsZero(record.ID) && fmt.Sprintf("%v", record.ID) != `0` {
					// convert incoming ID to it's destination field type
					queryGen.InputData[collection.IdentityField] = collection.ConvertValue(collection.IdentityField, record.ID)
				}

				// render the query into the final SQL
				if stmt, err := filter.Render(queryGen, collection.Name, filter.Null()); err == nil {
					querylog.Debugf("[%T] %s %v", self, string(stmt[:]), queryGen.GetValues())

					// execute the SQL
					if _, err := tx.Exec(string(stmt[:]), queryGen.GetValues()...); err != nil {
						defer tx.Rollback()
						return err
					}
				} else {
					defer tx.Rollback()
					return err
				}
			}

			// commit transaction
			if err := tx.Commit(); err == nil {
				if search := self.WithSearch(collection.Name); search != nil {
					if err := search.Index(collection.Name, recordset); err != nil {
						querylog.Debugf("[%T] index error %v", self, err)
					} else {
						return err
					}
				}

				return nil
			} else {
				return err
			}
		} else {
			return err
		}
	} else {
		return err
	}
}

func (self *SqlBackend) Exists(name string, id interface{}) bool {
	if collection, err := self.getCollectionFromCache(name); err == nil {
		if tx, err := self.db.Begin(); err == nil {
			defer tx.Commit()

			if f, err := filter.FromMap(map[string]interface{}{
				collection.IdentityField: fmt.Sprintf("is:%v", id),
			}); err == nil {
				f.Fields = []string{collection.IdentityField}
				queryGen := self.makeQueryGen(collection)

				if err := queryGen.Initialize(collection.Name); err == nil {
					if stmt, err := filter.Render(queryGen, collection.Name, f); err == nil {
						querylog.Debugf("[%T] %s %v", self, string(stmt[:]), queryGen.GetValues())

						// perform query
						if rows, err := tx.Query(string(stmt[:]), queryGen.GetValues()...); err == nil {
							defer rows.Close()
							return rows.Next()
						} else {
							querylog.Debugf("[%T] query error %v", self, err)
						}
					} else {
						querylog.Debugf("[%T] query generator error %v", self, err)
					}
				} else {
					querylog.Debugf("[%T] query generator error %v", self, err)
				}
			} else {
				querylog.Debugf("[%T] filter error %v", self, err)
			}
		} else {
			querylog.Debugf("[%T] transaction error %v", self, err)
		}
	} else {
		querylog.Debugf("[%T] cache error %v", self, err)
	}

	return false
}

func (self *SqlBackend) Retrieve(name string, id interface{}, fields ...string) (*dal.Record, error) {
	if collection, err := self.getCollectionFromCache(name); err == nil {
		if f, err := filter.FromMap(map[string]interface{}{
			collection.IdentityField: fmt.Sprintf("is:%v", id),
		}); err == nil {
			f.Fields = fields
			queryGen := self.makeQueryGen(collection)

			if err := queryGen.Initialize(collection.Name); err == nil {
				if stmt, err := filter.Render(queryGen, collection.Name, f); err == nil {
					querylog.Debugf("[%T] %s %v", self, string(stmt[:]), id)

					// perform query
					if rows, err := self.db.Query(string(stmt[:]), id); err == nil {
						defer rows.Close()

						if columns, err := rows.Columns(); err == nil {
							if rows.Next() {
								return self.scanFnValueToRecord(queryGen, collection, columns, reflect.ValueOf(rows.Scan), fields)
							} else {
								// if it doesn't exist, make sure it's not indexed
								if search := self.WithSearch(collection.Name); search != nil {
									defer search.IndexRemove(collection.Name, []interface{}{id})
								}

								return nil, fmt.Errorf("Record %v does not exist", id)
							}
						} else {
							return nil, err
						}
					} else {
						return nil, err
					}
				} else {
					return nil, err
				}
			} else {
				return nil, err
			}
		} else {
			return nil, err
		}
	} else {
		return nil, err
	}
}

func (self *SqlBackend) Update(name string, recordset *dal.RecordSet, target ...string) error {
	var targetFilter *filter.Filter

	if len(target) > 0 {
		if f, err := filter.Parse(target[0]); err == nil {
			targetFilter = f
		} else {
			return err
		}
	}

	if collection, err := self.getCollectionFromCache(name); err == nil {
		if tx, err := self.db.Begin(); err == nil {
			// for each record being updated...
			for _, record := range recordset.Records {
				if r, err := collection.MakeRecord(record); err == nil {
					record = r
				} else {
					return err
				}

				// setup query generator
				queryGen := self.makeQueryGen(collection)
				queryGen.Type = generators.SqlUpdateStatement

				var recordUpdateFilter *filter.Filter

				// if this record was specified without a specific ID, attempt to use the broader
				// target filter (if given)
				if record.ID == `` {
					if len(target) > 0 {
						recordUpdateFilter = targetFilter
					} else {
						defer tx.Rollback()
						return fmt.Errorf("Update must target at least one record")
					}
				} else {
					// try to build a filter targeting this specific record
					if f, err := filter.FromMap(map[string]interface{}{
						collection.IdentityField: fmt.Sprintf("is:%v", record.ID),
					}); err == nil {
						recordUpdateFilter = f
					} else {
						defer tx.Rollback()
						return err
					}
				}

				// add all non-ID fields to the record's Fields set
				for k, v := range record.Fields {
					if k != collection.IdentityField {
						queryGen.InputData[k] = v
					}
				}

				// generate SQL
				if stmt, err := filter.Render(queryGen, collection.Name, recordUpdateFilter); err == nil {
					querylog.Debugf("[%T] %s %v", self, string(stmt[:]), queryGen.GetValues())

					// execute SQL
					if _, err := tx.Exec(string(stmt[:]), queryGen.GetValues()...); err != nil {
						defer tx.Rollback()
						return err
					}
				} else {
					defer tx.Rollback()
					return err
				}
			}

			if err := tx.Commit(); err == nil {
				if search := self.WithSearch(collection.Name); search != nil {
					if err := search.Index(collection.Name, recordset); err != nil {
						return err
					}
				}

				return nil
			} else {
				return err
			}
		} else {
			return err
		}
	} else {
		return err
	}
}

func (self *SqlBackend) Delete(name string, ids ...interface{}) error {
	if collection, err := self.getCollectionFromCache(name); err == nil {
		// remove documents from index
		if search := self.WithSearch(collection.Name); search != nil {
			defer search.IndexRemove(collection.Name, ids)
		}

		f := filter.New()

		f.AddCriteria(filter.Criterion{
			Field:  collection.IdentityField,
			Values: ids,
		})

		if tx, err := self.db.Begin(); err == nil {
			queryGen := self.makeQueryGen(collection)
			queryGen.Type = generators.SqlDeleteStatement

			// generate SQL
			if stmt, err := filter.Render(queryGen, collection.Name, f); err == nil {
				querylog.Debugf("[%T] %s %v", self, string(stmt[:]), queryGen.GetValues())

				// execute SQL
				if _, err := tx.Exec(string(stmt[:]), queryGen.GetValues()...); err == nil {
					if err := tx.Commit(); err == nil {
						return nil
					} else {
						return err
					}
				} else {
					defer tx.Rollback()
					return err
				}
			} else {
				defer tx.Rollback()
				return err
			}
		} else {
			return err
		}
	} else {
		return err
	}
}

func (self *SqlBackend) WithSearch(collectionName string, filters ...*filter.Filter) Indexer {
	return self.indexer
}

func (self *SqlBackend) WithAggregator(collectionName string) Aggregator {
	if aggregator, ok := self.aggregator[collectionName]; ok {
		return aggregator
	}

	defaultAggregator, _ := self.aggregator[``]

	return defaultAggregator
}

func (self *SqlBackend) ListCollections() ([]string, error) {
	return maputil.StringKeys(&self.registeredCollections), nil
}

func (self *SqlBackend) CreateCollection(definition *dal.Collection) error {
	// -- sqlite3
	// CREATE TABLE foo (
	//     "id"         INTEGER PRIMARY KEY ASC,
	//     "name"       TEXT NOT NULL,
	//     "enabled"    INTEGER(1),
	//     "created_at" TEXT DEFAULT CURRENT_TIMESTAMP
	// );

	// -- MySQL
	// CREATE TABLE foo (
	//     `id`         INTEGER NOT NULL AUTO_INCREMENT PRIMARY KEY,
	//     `name`       TEXT NOT NULL,
	//     `enabled`    TINYINT(1),
	//     `created_at` DATETIME DEFAULT CURRENT_TIMESTAMP
	// );

	// -- PostgreSQL
	// CREATE TABLE foo (
	//     "id"         BIGSERIAL PRIMARY KEY,
	//     "name"       TEXT NOT NULL,
	//     "enabled"    BOOLEAN,
	//     "created_at" TIMESTAMP WITHOUT TIME ZONE DEFAULT now_utc()
	// );

	// -- MS SQL Server
	// CREATE TABLE [foo] (
	//     [id]         INT PRIMARY KEY IDENTITY(1,1) NOT NULL,
	//     [name]       NVARCHAR(MAX) NOT NULL,
	//     [enabled     BIT,
	//     [created_at] [DATETIME] DEFAULT CURRENT_TIMESTAMP
	// );

	if definition.IdentityField == `` {
		definition.IdentityField = dal.DefaultIdentityField
	}

	gen := self.makeQueryGen(definition)

	stmt := fmt.Sprintf("CREATE TABLE %s (", gen.ToTableName(definition.Name))

	fields := []string{}
	values := make([]interface{}, 0)

	if definition.IdentityField != `` {
		switch definition.IdentityFieldType {
		case dal.StringType:
			fields = append(fields, fmt.Sprintf(self.createPrimaryKeyStrFormat, gen.ToFieldName(definition.IdentityField)))
		default:
			fields = append(fields, fmt.Sprintf(self.createPrimaryKeyIntFormat, gen.ToFieldName(definition.IdentityField)))
		}
	}

	for _, field := range definition.Fields {
		var def string

		// This is weird...
		//
		// So Raw fields and Object fields are stored using the same datatype (BLOB), which
		// means that when we read back the schema definition, we don't have a decisive way of
		// knowing whether that field should be treated as Raw or Object.  So we create Object fields
		// with a specific length.  This serves as a hint to us that we should treat this field as an object field.
		//
		// We could also do this with comments, but not all SQL servers necessarily support comments on
		// table schemata, so this feels more reliable in practical usage.
		//
		if field.Type == dal.ObjectType {
			field.Length = objectFieldHintLength
		}

		if nativeType, err := gen.ToNativeType(field.Type, []dal.Type{field.Subtype}, field.Length); err == nil {
			def = fmt.Sprintf("%s %s", gen.ToFieldName(field.Name), nativeType)
		} else {
			return err
		}

		if field.Required {
			def += ` NOT NULL`
		}

		if field.Unique {
			def += ` UNIQUE`
		}

		// if the default value is neither nil nor a function
		if v := field.DefaultValue; v != nil && !typeutil.IsFunction(field.DefaultValue) {
			def += fmt.Sprintf(" DEFAULT %v", gen.ToNativeValue(field.Type, []dal.Type{field.Subtype}, v))
		}

		fields = append(fields, def)
	}

	stmt += strings.Join(fields, `, `)
	stmt += `)`

	if tx, err := self.db.Begin(); err == nil {
		querylog.Debugf("[%T] %s %v", self, string(stmt[:]), values)

		if _, err := tx.Exec(stmt, values...); err == nil {
			defer func() {
				self.RegisterCollection(definition)

				if err := self.refreshCollectionFromDatabase(definition.Name, definition); err != nil {
					querylog.Debugf("[%T] failed to refresh collection: %v", self, err)
				}
			}()
			return tx.Commit()
		} else {
			defer tx.Rollback()
			return err
		}
	} else {
		return err
	}
}

func (self *SqlBackend) DeleteCollection(collectionName string) error {
	if collection, err := self.getCollectionFromCache(collectionName); err == nil {
		gen := self.makeQueryGen(collection)

		if tx, err := self.db.Begin(); err == nil {
			stmt := fmt.Sprintf(self.dropTableQuery, gen.ToTableName(collectionName))
			querylog.Debugf("[%T] %s", self, string(stmt[:]))

			if _, err := tx.Exec(stmt); err == nil {
				return tx.Commit()
			} else {
				defer tx.Rollback()
				return err
			}
		} else {
			return err
		}
	} else {
		return err
	}
}

func (self *SqlBackend) GetCollection(name string) (*dal.Collection, error) {
	if err := self.refreshCollectionFromDatabase(name, nil); err == nil {
		if _, ok := self.knownCollections[name]; !ok {
			return nil, dal.CollectionNotFound
		}

		if collection, err := self.getCollectionFromCache(name); err == nil {
			return collection, nil
		} else {
			return nil, err
		}
	} else {
		return nil, err
	}
}

func (self *SqlBackend) Flush() error {
	if self.indexer != nil {
		return self.indexer.FlushIndex()
	}

	return nil
}

func (self *SqlBackend) makeQueryGen(collection *dal.Collection) *generators.Sql {
	queryGen := generators.NewSqlGenerator()
	queryGen.TypeMapping = self.queryGenTypeMapping

	if v := self.queryGenPlaceholderFormat; v != `` {
		queryGen.PlaceholderFormat = v
	}

	if v := self.queryGenPlaceholderArgument; v != `` {
		queryGen.PlaceholderArgument = v
	}

	if v := self.queryGenTableFormat; v != `` {
		queryGen.TableNameFormat = v
	}

	if v := self.queryGenFieldFormat; v != `` {
		queryGen.FieldNameFormat = v
	}

	if v := self.queryGenNestedFieldFormat; v != `` {
		queryGen.NestedFieldNameFormat = v
	}

	if collection != nil {
		// perform string normalization on non-pk, non-key string fields
		for _, field := range collection.Fields {
			if field.Identity || field.Key {
				continue
			}

			if field.Type == dal.StringType {
				queryGen.NormalizeFields = append(queryGen.NormalizeFields, field.Name)
			}
		}

		// set the format for string normalization
		if v := self.queryGenNormalizerFormat; v != `` {
			queryGen.NormalizerFormat = v
		}
	}

	return queryGen
}

func (self *SqlBackend) scanFnValueToRecord(queryGen *generators.Sql, collection *dal.Collection, columns []string, scanFn reflect.Value, wantedFields []string) (*dal.Record, error) {
	if scanFn.Kind() != reflect.Func {
		return nil, fmt.Errorf("Can only accept a function value")
	}

	// sql.Row.Scan is strict about how we call it (e.g.: won't return results as a map),
	// so we hack...
	//
	output := make([]interface{}, len(columns))

	// put a zero-value instance of each column's type in the result array, which will
	// serve as a hint to the sql.Scan function as to how to convert the data
	for i, column := range columns {
		baseColumn := strings.Split(column, queryGen.NestedFieldSeparator)[0]

		if field, ok := collection.GetField(baseColumn); ok {
			if field.DefaultValue != nil {
				output[i] = field.GetDefaultValue()
			} else if field.Required {
				output[i] = field.GetTypeInstance()
			} else {
				switch field.Type {
				case dal.StringType, dal.TimeType, dal.ObjectType:
					output[i] = sql.NullString{}

				case dal.BooleanType:
					output[i] = sql.NullBool{}

				case dal.IntType:
					output[i] = sql.NullInt64{}

				case dal.FloatType:
					output[i] = sql.NullFloat64{}

				default:
					output[i] = make([]byte, 0)
				}
			}
		} else {
			querylog.Warningf("[%T] Collection '%s' does not have a field called '%s'", self, collection.Name, baseColumn)
		}
	}

	rRowArgs := make([]reflect.Value, len(output))

	// each argument in the call to scan will be the address of the corresponding
	// item in the output array
	for i, _ := range output {
		rRowArgs[i] = reflect.ValueOf(output).Index(i).Addr()
	}

	// perform the call to the Scan() function with the correct number of "arguments"
	rRowResult := scanFn.Call(rRowArgs)

	var err error

	// the function should only return one value of type error
	if len(rRowResult) == 1 {
		v := rRowResult[0].Interface()

		if e, ok := v.(error); ok || v == nil {
			err = e
		} else {
			return nil, fmt.Errorf("row scan call returned invalid type (%T)", v)
		}
	} else {
		return nil, fmt.Errorf("invalid response from row scan call")
	}

	// this is the actual error returned from calling Scan()
	if err == nil {
		var id interface{}
		fields := make(map[string]interface{})

		// for each column in the resultset

	ColumnLoop:
		for i, column := range columns {
			nestedPath := strings.Split(column, queryGen.NestedFieldSeparator)
			baseColumn := nestedPath[0]

			if field, ok := collection.GetField(baseColumn); ok {
				var value interface{}

				// convert value types as needed
				switch output[i].(type) {
				// raw byte arrays will either be strings, blobs, or binary-encoded objects
				// we need to figure out which
				case []uint8:
					v := output[i].([]uint8)

					var dest map[string]interface{}

					switch field.Type {
					case dal.ObjectType:
						if err := generators.SqlObjectTypeDecode([]byte(v[:]), &dest); err == nil {
							value = dest
						} else {
							value = string(v[:])
						}

					// if this field is a raw type, then it's not a string, which
					// leaves raw or object
					//
					case dal.RawType:
						// blindly attempt to load the data as if it were an object, then
						// fallback to using the raw byte array
						//
						if err := generators.SqlObjectTypeDecode([]byte(v[:]), &dest); err == nil {
							value = dest
						} else {
							value = []byte(v[:])
						}

					default:
						value = string(v[:])
					}
				case sql.NullString:
					v := output[i].(sql.NullString)

					if v.Valid {
						value = v.String
					} else {
						value = nil
					}

				case sql.NullBool:
					v := output[i].(sql.NullBool)

					if v.Valid {
						value = v.Bool
					} else {
						value = nil
					}

				case sql.NullInt64:
					v := output[i].(sql.NullInt64)

					if v.Valid {
						value = v.Int64
					} else {
						value = nil
					}

				case sql.NullFloat64:
					v := output[i].(sql.NullFloat64)

					if v.Valid {
						value = v.Float64
					} else {
						value = nil
					}
				default:
					value = output[i]
				}

				// set the appropriate field for the dal.Record
				if v, err := field.ConvertValue(value); err == nil {
					if column == collection.IdentityField {
						id = v
					} else {
						if len(wantedFields) > 0 {
							shouldSkip := true

							for _, wantedField := range wantedFields {
								parts := strings.Split(wantedField, queryGen.NestedFieldSeparator)

								if parts[0] == baseColumn {
									shouldSkip = false
									break
								}
							}

							if shouldSkip {
								continue ColumnLoop
							}

						}

						if newFields, ok := maputil.DeepSet(fields, nestedPath, v).(map[string]interface{}); ok {
							fields = newFields
						}
					}
				} else {
					querylog.Warningf("[%T] Collection %s: field %v: %v", self, collection.Name, baseColumn, err)
				}
			}
		}

		record := dal.NewRecord(id).SetFields(fields)

		// do this AFTER populating the record's fields from the database
		if err := record.Populate(record, collection); err != nil {
			return nil, fmt.Errorf("error populating record: %v", err)
		}

		return record, nil
	} else {
		return nil, err
	}
}

// func (self *SqlBackend) Migrate(diff []dal.SchemaDelta) error {
// 	for _, delta := range diff {
// 		switch delta.Issue {
// 		case dal.CollectionKeyNameIssue, dal.CollectionKeyTypeIssue:
// 			return fmt.Errorf("Cannot alter key name or type for %T", self)

// 		case dal.FieldMissingIssue:
// 			// ALTER TABLE ADD COLUMN ...
// 			if collection, err := self.getCollectionFromCache(delta.Collection); err == nil {
// 				if field, ok := collection.GetField(delta.Name); ok {

// 				} else {
// 					return fmt.Errorf("Cannot add field %q: not in collection %q", delta.Name, delta.Collection)
// 				}
// 			} else {
// 				return fmt.Errorf("Cannot add field %q: %v", delta.Name, err)
// 			}

// 		case dal.FieldNameIssue:
// 			// ALTER TABLE ADD COLUMN ...
// 		case dal.FieldLengthIssue:
// 			// ALTER TABLE  ...
// 		case dal.FieldTypeIssue:
// 			// ALTER TABLE  ...
// 		case dal.FieldPropertyIssue:
// 			// ...
// 		}
// 	}

// 	return fmt.Errorf("Not Implemented")
// }

func (self *SqlBackend) refreshAllCollections() error {
	if rows, err := self.db.Query(self.listAllTablesQuery); err == nil {
		defer rows.Close()
		knownTables := make([]string, 0)

		// refresh all tables that come back from the list all tables query
		for rows.Next() {
			var tableName string

			if err := rows.Scan(&tableName); err == nil {
				if definitionI, ok := self.registeredCollections.Load(tableName); ok {
					definition := definitionI.(*dal.Collection)
					knownTables = append(knownTables, definition.Name)

					if err := self.refreshCollectionFromDatabase(definition.Name, definition); err != nil {
						log.Errorf("Error refreshing collection %s: %v", definition.Name, err)
					}
				} else {
					if err := self.refreshCollectionFromDatabase(tableName, nil); err != nil {
						log.Errorf("Error refreshing collection %s: %v", tableName, err)
					}
				}
			} else {
				log.Errorf("Error refreshing collection %s: %v", tableName, err)
			}
		}

		if !self.conn.OptBool(`autoregister`, DefaultAutoregister) {
			// purge from cache any tables that the list all query didn't return
			self.registeredCollections.Range(func(key, value interface{}) bool {
				if !sliceutil.ContainsString(knownTables, key.(string)) {
					self.registeredCollections.Delete(key)
				}

				return true
			})
		}

		return rows.Err()
	} else {
		return err
	}
}

func (self *SqlBackend) refreshCollectionFromDatabase(name string, definition *dal.Collection) error {
	if collection, err := self.refreshCollectionFunc(
		self.conn.Dataset(),
		name,
	); err == nil {
		if len(collection.Fields) > 0 {
			if definition != nil {
				// we've read the collection back from the database, but in the process we've lost
				// some local values that only existed on the definition itself.  we need to copy those into
				// the collection that just came back
				collection.ApplyDefinition(definition)
				self.RegisterCollection(definition)

			} else if self.conn.OptBool(`autoregister`, DefaultAutoregister) {
				self.RegisterCollection(collection)
			}

			self.knownCollections[name] = true
		}

		return nil
	} else {
		return err
	}
}

func (self *SqlBackend) getCollectionFromCache(name string) (*dal.Collection, error) {
	if registered, ok := self.registeredCollections.Load(name); ok {
		return registered.(*dal.Collection), nil
	} else {
		return nil, dal.CollectionNotFound
	}
}
