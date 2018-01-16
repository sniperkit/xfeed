package main

import (
	"github.com/sniperkit/xfeed/plugin/api/plugin"

	"github.com/sniperkit/xfeed/plugin/scenario/ann"
)

func main() {
	server, err := plugin.Server()
	if err != nil {
		panic(err)
	}
	server.RegisterPipeline(new(ann.Scenario))
	server.Serve()
}
