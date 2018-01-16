package null

import (
	"github.com/sniperkit/xfeed/plugin/api/common"
	"github.com/sniperkit/xfeed/plugin/api/workflow"
	//	"github.com/sniperkit/xfeed/plugin/api/plugin/model"
)

type config struct {
	common.ElasticfeedConfig `mapstructure:",squash"`

	tpl *workflow.ConfigTemplate
}

type Pipeline struct {
	config config
}

func (p *Pipeline) Prepare(raws ...interface{}) ([]string, error) {
	return nil, nil
}

func (p *Pipeline) Run(data interface{}) (interface{}, error) {
	return nil, nil
}

func (p *Pipeline) Cancel() {
}
