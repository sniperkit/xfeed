package main

import (
	"github.com/sniperkit/xfeed/plugin/api/plugin"

	"github.com/sniperkit/xfeed/plugin/pipeline/ann"
)

func main() {
	server, err := plugin.Server()
	if err != nil {
		panic(err)
	}
	server.RegisterPipeline(new(ann.Pipeline))
	server.Serve()
}
