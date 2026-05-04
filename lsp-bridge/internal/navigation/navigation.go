package navigation

import "github.com/jgibbarduk/zed-atmos-extension/lsp-bridge/internal/index"

type Location struct {
	URI   string      `json:"uri"`
	Range index.Range `json:"range"`
}

type Navigator struct {
	idx *index.Index
}

func New(idx *index.Index) *Navigator {
	return &Navigator{idx: idx}
}

func (n *Navigator) GoToDefinition(uri string, line, char uint32) ([]Location, error) {
	return nil, nil
}

func (n *Navigator) FindReferences(uri string, line, char uint32) ([]Location, error) {
	return nil, nil
}
