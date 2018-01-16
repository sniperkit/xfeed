package main

import (
	"github.com/sniperkit/xfeed/plugin/api/elasticfeed"
)

func main() {
	engine := elasticfeed.NewElasticfeed()
	engine.Run()
}
