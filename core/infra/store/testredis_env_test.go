package store

import (
	"os"
	"testing"

	"github.com/cordum/cordum/core/internal/testredis"
)

func TestMain(m *testing.M) {
	restore := testredis.ApplyPoolEnv()
	code := m.Run()
	restore()
	os.Exit(code)
}
