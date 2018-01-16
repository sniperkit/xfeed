package service

import (
	"errors"

	"github.com/sniperkit/xfeed/pkg/graph"
)

type GraphService struct {
	Storage *graph.GraphStorage
}

func (s *GraphService) Name() string {
	return "graph-service"
}

func NewGraph() (*GraphService, error) {
	storage, err := graph.NewGraphStorage()
	if err != nil {
		return nil, errors.New("Cannot load graph storage")
	}

	return &GraphService{storage}, nil
}
