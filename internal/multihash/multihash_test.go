package multihash_test

//import (
//	"crypto/md5"
//	"crypto/sha256"
//	"testing"

//	gc "launchpad.net/gocheck"

//	"github.com/juju/charmstore/internal/multihash"
//)

//func TestPackage(t *testing.T) {
//	gc.TestingT(t)
//}

//type MultiHashSuite struct{}

//var _ = gc.Suite(&MultiHashSuite{})

//func (*MultiHashSuite) TestMultiHash(c *gc.C) {
//	h := multihash.New(md5.New(), sha256.New())
//	c.Assert(h.Size(), gc.Equals, md5.Size+sha256.Size)
//	c.Assert(h.BlockSize(), gc.Equals, md5.BlockSize)
//	text := []byte("hello")
//	n, err := h.Write(text)
//	c.Assert(err, gc.Equals, nil)
//	c.Assert(n, gc.Equals, 5)

//	sum := h.Sum(nil)
//	md5sum := md5.Sum(text)
//	sha256sum := sha256.Sum256(text)
//	c.Assert(sum, gc.HasLen, h.Size())
//	c.Assert(sum[0:md5.Size], gc.DeepEquals, md5sum[:])
//	c.Assert(sum[md5.Size:], gc.DeepEquals, sha256sum[:])

//	h.Reset()
//	h.Write(text)
//	c.Assert(h.Sum(nil), gc.DeepEquals, sum)
//}
