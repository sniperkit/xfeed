package main

import (
	"github.com/sniperkit/xfeed/plugin/api/plugin"

	"github.com/sniperkit/xfeed/plugin/pipeline/null"
)

func main() {
	server, err := plugin.Server()
	if err != nil {
		panic(err)
	}
	server.RegisterPipeline(new(null.Pipeline))
	server.Serve()
}
