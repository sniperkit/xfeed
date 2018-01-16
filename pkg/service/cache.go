package service

import (
	"github.com/sniperkit/xfeed/plugin/backend/memcache"
)

func NewCache() *memcache.MemcacheClient {
	return memcache.NewMemcacheClient()
}
