package codec_test

import (
	"testing"

	"github.com/fastabc/fastconf/codec"
)

func TestRegister_NilPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on nil codec registration")
		}
	}()
	codec.Register("nilcodec", nil)
}
