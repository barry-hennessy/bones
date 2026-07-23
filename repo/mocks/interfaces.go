package mocks

import (
	"context"

	"github.com/barry-hennessy/bones/repo"
)

//go:generate go run github.com/matryer/moq@latest -stub -with-resets -out moq_gen.go . Tx TestFactory TestDirectFactory

type TestRepos struct {
	Value string
}

type Tx interface {
	Commit(context.Context) error
	Rollback(context.Context) error
	Close(context.Context) error
}

type TestFactory interface {
	Begin(context.Context) (repo.Tx, TestRepos, error)
}

type TestDirectFactory interface {
	Open(context.Context) (TestRepos, func(context.Context) error, error)
}
