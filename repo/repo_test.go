package repo_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/barry-hennessy/bones/repo"
	"github.com/barry-hennessy/bones/repo/mocks"
)

func TestTransact_Success(t *testing.T) {
	t.Parallel()

	var events []string
	tx := &mocks.TxMock{
		CommitFunc: func(context.Context) error {
			events = append(events, "commit")
			return nil
		},
		RollbackFunc: func(context.Context) error {
			events = append(events, "rollback")
			return nil
		},
		CloseFunc: func(context.Context) error {
			events = append(events, "close")
			return nil
		},
	}
	factory := &mocks.TestFactoryMock{
		BeginFunc: func(context.Context) (repo.Tx, mocks.TestRepos, error) {
			return tx, mocks.TestRepos{Value: "hello"}, nil
		},
	}

	result, err := repo.Transact(context.Background(), factory,
		func(_ context.Context, r mocks.TestRepos) (string, error) {
			return r.Value + " world", nil
		},
	)

	require.NoError(t, err)
	assert.Equal(t, "hello world", result)
	assert.Len(t, factory.BeginCalls(), 1)
	assert.Len(t, tx.CommitCalls(), 1)
	assert.Len(t, tx.RollbackCalls(), 1)
	assert.Len(t, tx.CloseCalls(), 1)
	assert.Equal(t, []string{"commit", "rollback", "close"}, events)
}

func TestTransact_FnError_Rollback(t *testing.T) {
	t.Parallel()

	fnErr := errors.New("business logic failed")
	tx := &mocks.TxMock{}
	factory := &mocks.TestFactoryMock{
		BeginFunc: func(context.Context) (repo.Tx, mocks.TestRepos, error) {
			return tx, mocks.TestRepos{}, nil
		},
	}

	_, err := repo.Transact(context.Background(), factory,
		func(_ context.Context, _ mocks.TestRepos) (string, error) {
			return "", fnErr
		},
	)

	require.ErrorIs(t, err, fnErr)
	assert.Len(t, tx.CommitCalls(), 0)
	assert.Len(t, tx.RollbackCalls(), 1)
	assert.Len(t, tx.CloseCalls(), 1)
}

func TestTransact_CommitError_Rollback(t *testing.T) {
	t.Parallel()

	commitErr := errors.New("commit failed")
	tx := &mocks.TxMock{
		CommitFunc: func(context.Context) error {
			return commitErr
		},
	}
	factory := &mocks.TestFactoryMock{
		BeginFunc: func(context.Context) (repo.Tx, mocks.TestRepos, error) {
			return tx, mocks.TestRepos{}, nil
		},
	}

	_, err := repo.Transact(context.Background(), factory,
		func(_ context.Context, _ mocks.TestRepos) (string, error) {
			return "ok", nil
		},
	)

	require.ErrorIs(t, err, commitErr)
	assert.Len(t, tx.CommitCalls(), 1)
	assert.Len(t, tx.RollbackCalls(), 1)
	assert.Len(t, tx.CloseCalls(), 1)
}

func TestTransact_BeginError(t *testing.T) {
	t.Parallel()

	beginErr := errors.New("could not start transaction")
	factory := &mocks.TestFactoryMock{
		BeginFunc: func(context.Context) (repo.Tx, mocks.TestRepos, error) {
			return nil, mocks.TestRepos{}, beginErr
		},
	}
	fnCalled := false

	_, err := repo.Transact(context.Background(), factory,
		func(_ context.Context, _ mocks.TestRepos) (string, error) {
			fnCalled = true
			return "unreachable", nil
		},
	)

	require.ErrorIs(t, err, beginErr)
	assert.False(t, fnCalled)
	assert.Len(t, factory.BeginCalls(), 1)
}

func TestTransact_Panic(t *testing.T) {
	t.Parallel()

	var events []string
	tx := &mocks.TxMock{
		CommitFunc: func(context.Context) error {
			events = append(events, "commit")
			return nil
		},
		RollbackFunc: func(context.Context) error {
			events = append(events, "rollback")
			return nil
		},
		CloseFunc: func(context.Context) error {
			events = append(events, "close")
			return nil
		},
	}
	factory := &mocks.TestFactoryMock{
		BeginFunc: func(context.Context) (repo.Tx, mocks.TestRepos, error) {
			return tx, mocks.TestRepos{}, nil
		},
	}

	defer func() {
		r := recover()
		require.Equal(t, "boom", r)
		assert.Len(t, tx.CommitCalls(), 0)
		assert.Len(t, tx.RollbackCalls(), 1)
		assert.Len(t, tx.CloseCalls(), 1)
		assert.Equal(t, []string{"rollback", "close"}, events)
	}()

	_, _ = repo.Transact(context.Background(), factory,
		func(_ context.Context, _ mocks.TestRepos) (string, error) {
			panic("boom")
		},
	)
}

func TestRun_Success(t *testing.T) {
	t.Parallel()

	var events []string
	factory := &mocks.TestDirectFactoryMock{
		OpenFunc: func(context.Context) (mocks.TestRepos, func(context.Context) error, error) {
			return mocks.TestRepos{Value: "hello"}, func(context.Context) error {
				events = append(events, "release")
				return nil
			}, nil
		},
	}

	result, err := repo.Run(context.Background(), factory,
		func(_ context.Context, r mocks.TestRepos) (string, error) {
			events = append(events, "fn")
			return r.Value + " world", nil
		},
	)

	require.NoError(t, err)
	assert.Equal(t, "hello world", result)
	assert.Len(t, factory.OpenCalls(), 1)
	assert.Equal(t, []string{"fn", "release"}, events)
}

func TestRun_OpenError(t *testing.T) {
	t.Parallel()

	openErr := errors.New("could not open direct repos")
	factory := &mocks.TestDirectFactoryMock{
		OpenFunc: func(context.Context) (mocks.TestRepos, func(context.Context) error, error) {
			return mocks.TestRepos{}, nil, openErr
		},
	}

	fnCalled := false
	_, err := repo.Run(context.Background(), factory,
		func(_ context.Context, _ mocks.TestRepos) (string, error) {
			fnCalled = true
			return "unreachable", nil
		},
	)

	require.ErrorIs(t, err, openErr)
	assert.False(t, fnCalled)
	assert.Len(t, factory.OpenCalls(), 1)
}

func TestRun_FnError_Release(t *testing.T) {
	t.Parallel()

	fnErr := errors.New("business logic failed")
	var events []string
	factory := &mocks.TestDirectFactoryMock{
		OpenFunc: func(context.Context) (mocks.TestRepos, func(context.Context) error, error) {
			return mocks.TestRepos{}, func(context.Context) error {
				events = append(events, "release")
				return nil
			}, nil
		},
	}

	_, err := repo.Run(context.Background(), factory,
		func(_ context.Context, _ mocks.TestRepos) (string, error) {
			events = append(events, "fn")
			return "", fnErr
		},
	)

	require.ErrorIs(t, err, fnErr)
	assert.Len(t, factory.OpenCalls(), 1)
	assert.Equal(t, []string{"fn", "release"}, events)
}

func TestRun_Panic(t *testing.T) {
	t.Parallel()

	var events []string
	factory := &mocks.TestDirectFactoryMock{
		OpenFunc: func(context.Context) (mocks.TestRepos, func(context.Context) error, error) {
			return mocks.TestRepos{}, func(context.Context) error {
				events = append(events, "release")
				return nil
			}, nil
		},
	}

	defer func() {
		r := recover()
		require.Equal(t, "boom", r)
		assert.Len(t, factory.OpenCalls(), 1)
		assert.Equal(t, []string{"fn", "release"}, events)
	}()

	_, _ = repo.Run(context.Background(), factory,
		func(_ context.Context, _ mocks.TestRepos) (string, error) {
			events = append(events, "fn")
			panic("boom")
		},
	)
}
