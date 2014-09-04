//package multihash
//
//import (
//	"hash"
//)

// New returns a hash.Hash that combines h1 and h2;
// the resulting checksum will contain the concatenation
// of the checksums from h1 and h2, in that order.
//func New(h1, h2 hash.Hash) hash.Hash {
//	return &multiHash{h1, h2}
//}

//type multiHash struct {
//	h1, h2 hash.Hash
//}

//func (h *multiHash) Write(buf []byte) (int, error) {
	// Note: Hash.Write never returns an error.
	// See http://golang.org/pkg/hash/#Hash
//	h.h1.Write(buf)
//	h.h2.Write(buf)
//	return len(buf), nil
//}

//func (h *multiHash) Sum(buf []byte) []byte {
//	buf = h.h1.Sum(buf)
//	buf = h.h2.Sum(buf)
//	return buf
//}

//func (h *multiHash) Reset() {
//	h.h1.Reset()
//	h.h2.Reset()
//}

//func (h *multiHash) Size() int {
//	return h.h1.Size() + h.h2.Size()
//}

//func (h *multiHash) BlockSize() int {
	// better: use least common multiple of both block sizes.
//	return h.h1.BlockSize()
//}
