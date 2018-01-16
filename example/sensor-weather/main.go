package main

import (
	"github.com/sniperkit/xfeed/plugin/api/plugin"

	"github.com/sniperkit/xfeed/plugin/sensor/weather"
)

func main() {
	server, err := plugin.Server()
	if err != nil {
		panic(err)
	}
	server.RegisterPipeline(new(weather.Sensor))
	server.Serve()
}
