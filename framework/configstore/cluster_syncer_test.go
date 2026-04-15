package configstore

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateRedisStreamCursor(t *testing.T) {
	t.Parallel()
	assert.NoError(t, validateRedisStreamCursor("0-0"))
	assert.NoError(t, validateRedisStreamCursor("1700000000000-0"))
	assert.NoError(t, validateRedisStreamCursor("1-2"))
	assert.Error(t, validateRedisStreamCursor("corrupted"))
	assert.Error(t, validateRedisStreamCursor("-1-0"))
	assert.Error(t, validateRedisStreamCursor("abc-def"))
}
