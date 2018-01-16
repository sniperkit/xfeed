package ann

import (
	"github.com/sniperkit/xfeed/plugin/api/common"
	"github.com/sniperkit/xfeed/plugin/api/workflow"
	//	"github.com/sniperkit/xfeed/plugin/api/plugin/model"
)

type config struct {
	common.ElasticfeedConfig `mapstructure:",squash"`

	tpl *workflow.ConfigTemplate
}

type Scenario struct {
	config config
}

func (p *Scenario) Prepare(raws ...interface{}) ([]string, error) {
	return nil, nil
}

func (p *Scenario) Run(data interface{}) (interface{}, error) {
	return data, nil
}

func (p *Scenario) Cancel() {
}
