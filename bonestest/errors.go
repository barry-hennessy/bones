package bonestest

import (
	"testing"

	"github.com/barry-hennessy/bones"

	"github.com/stretchr/testify/assert"
)

type ErrMapperTest[T any] struct {
	Name     string
	Err      error
	Expected T
}

func TestErrMapper[T any](t *testing.T, mapper bones.ErrMapper[T], tests []ErrMapperTest[T]) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.Name, func(t *testing.T) {
			res := mapper(tt.Err)
			assert.Equal(t, tt.Expected, res)
		})
	}
}
