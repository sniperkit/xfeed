package xfeed

import (
	"fmt"
	"strconv"

	"github.com/astaxie/beego"

	"github.com/sniperkit/xfeed/pkg/config"
	_ "github.com/sniperkit/xfeed/pkg/graph/adapter"
	_ "github.com/sniperkit/xfeed/pkg/stream/adapter/message"
)

func GetConfigKey(key string) string {
	return config.GetConfigKey(key)
}

func Banner() {
	fmt.Printf("Starting app '%s' on port '%s'\n", config.GetConfigKey("appname"), config.GetConfigKey("xfeed::port"))
}

func SetStaticPath(url string, path string) *beego.App {
	return beego.SetStaticPath(url, path)
}

func Error(v ...interface{}) {
	beego.Error(v...)
}

func Run() {
	Banner()

	beego.HttpPort, _ = strconv.Atoi(config.GetConfigKey("xfeed::port"))
	beego.Run()
}
