package wszero

import (
	"encoding/binary"
	"testing"
)

func xorMaskBytewise(b []byte, key32 uint32) {
	key := [4]byte{}
	binary.LittleEndian.PutUint32(key[:], key32)
	for i := range b {
		b[i] ^= key[i%4]
	}
}

func TestXorMask(t *testing.T) {
	t.Parallel()
	const key = uint32(0xDDCCBBAA)
	for sz := 0; sz < 1024; sz++ {
		b0 := make([]byte, sz+wordSize-1)
		for algn := 0; algn < wordSize; algn++ {
			b := b0[algn:][:sz]
			xorMaskBytewise(b, key^0x12121212)
			xorMask(b, key)
			for i := range b {
				if b[i] != 0x12 {
					t.Errorf("size:%d, align:%d, offset:%d", sz, algn, i)
				}
				b[i] = 0
			}
		}
	}
}
