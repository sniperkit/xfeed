package main

import (
	"github.com/sniperkit/xfeed/plugin/api/plugin"

	imagga "github.com/sniperkit/xfeed/plugin/indexer/photo-imagga"
)

func main() {
	server, err := plugin.Server()
	if err != nil {
		panic(err)
	}
	server.RegisterPipeline(new(imagga.Indexer))
	server.Serve()
}
