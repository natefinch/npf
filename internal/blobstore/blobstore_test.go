package blobstore_test

import (
	"fmt"
	"io"
	"io/ioutil"
	"strconv"
	"strings"
	"testing"

	jujutesting "github.com/juju/testing"
	gc "launchpad.net/gocheck"

	"github.com/juju/charmstore/internal/blobstore"
	"github.com/juju/charmstore/internal/storetesting"
)

func TestPackage(t *testing.T) {
	jujutesting.MgoTestPackage(t, nil)
}

type BlobStoreSuite struct {
	storetesting.IsolatedMgoSuite
}

var _ = gc.Suite(&BlobStoreSuite{})

func (s *BlobStoreSuite) TestPutOpen(c *gc.C) {
	store := blobstore.New(s.Session.DB("db"), "blobstore")
	content := "some data"
	chal, err := store.Put(strings.NewReader(content), "x", int64(len(content)), hashOf(content), nil)
	c.Assert(err, gc.IsNil)
	c.Assert(chal, gc.IsNil)

	rc, length, err := store.Open("x")
	c.Assert(err, gc.IsNil)
	defer rc.Close()
	c.Assert(length, gc.Equals, int64(len(content)))

	data, err := ioutil.ReadAll(rc)
	c.Assert(err, gc.IsNil)
	c.Assert(string(data), gc.Equals, content)

	// Putting the resource again should generate a challenge.
	chal, err = store.Put(strings.NewReader(content), "y", int64(len(content)), hashOf(content), nil)
	c.Assert(err, gc.IsNil)
	c.Assert(chal, gc.NotNil)

	resp, err := blobstore.NewContentChallengeResponse(chal, strings.NewReader(content))
	c.Assert(err, gc.IsNil)

	chal, err = store.Put(strings.NewReader(content), "y", int64(len(content)), hashOf(content), resp)
	c.Assert(err, gc.IsNil)
	c.Assert(chal, gc.IsNil)
}

func (s *BlobStoreSuite) TestPutInvalidHash(c *gc.C) {
	store := blobstore.New(s.Session.DB("db"), "blobstore")
	content := "some data"
	chal, err := store.Put(strings.NewReader(content), "x", int64(len(content)), hashOf("wrong"), nil)
	c.Assert(err, gc.ErrorMatches, "hash mismatch")
	c.Assert(chal, gc.IsNil)

	rc, length, err := store.Open("x")
	c.Assert(err, gc.ErrorMatches, "resource.*not found")
	c.Assert(rc, gc.Equals, nil)
	c.Assert(length, gc.Equals, int64(0))
}

func (s *BlobStoreSuite) TestPutUnchallenged(c *gc.C) {
	store := blobstore.New(s.Session.DB("db"), "blobstore")

	content := "some data"
	err := store.PutUnchallenged(strings.NewReader(content), "x", int64(len(content)), hashOf(content))
	c.Assert(err, gc.IsNil)

	rc, length, err := store.Open("x")
	c.Assert(err, gc.IsNil)
	defer rc.Close()
	c.Assert(length, gc.Equals, int64(len(content)))

	data, err := ioutil.ReadAll(rc)
	c.Assert(err, gc.IsNil)
	c.Assert(string(data), gc.Equals, content)

	err = store.PutUnchallenged(strings.NewReader(content), "x", int64(len(content)), hashOf(content))
	c.Assert(err, gc.IsNil)
}

func (s *BlobStoreSuite) TestPutUnchallengedInvalidHash(c *gc.C) {
	store := blobstore.New(s.Session.DB("db"), "blobstore")
	content := "some data"
	err := store.PutUnchallenged(strings.NewReader(content), "x", int64(len(content)), hashOf("wrong"))
	c.Assert(err, gc.ErrorMatches, "hash mismatch")
}

func (s *BlobStoreSuite) TestRemove(c *gc.C) {
	store := blobstore.New(s.Session.DB("db"), "blobstore")
	content := "some data"
	err := store.PutUnchallenged(strings.NewReader(content), "x", int64(len(content)), hashOf(content))
	c.Assert(err, gc.IsNil)

	rc, length, err := store.Open("x")
	c.Assert(err, gc.IsNil)
	defer rc.Close()
	c.Assert(length, gc.Equals, int64(len(content)))
	data, err := ioutil.ReadAll(rc)
	c.Assert(err, gc.IsNil)
	c.Assert(string(data), gc.Equals, content)

	err = store.Remove("x")
	c.Assert(err, gc.IsNil)

	rc, length, err = store.Open("x")
	c.Assert(err, gc.ErrorMatches, `resource at path "[^"]+" not found`)
}

func (s *BlobStoreSuite) TestLarge(c *gc.C) {
	store := blobstore.New(s.Session.DB("db"), "blobstore")
	size := int64(20 * 1024 * 1024)
	newContent := func() io.Reader {
		return newDataSource(123, size)
	}
	hash := hashOfReader(c, newContent())

	chal, err := store.Put(newContent(), "x", size, hash, nil)
	c.Assert(err, gc.IsNil)
	c.Assert(chal, gc.IsNil)

	rc, length, err := store.Open("x")
	c.Assert(err, gc.IsNil)
	defer rc.Close()
	c.Assert(length, gc.Equals, size)

	c.Assert(hashOfReader(c, rc), gc.Equals, hash)
}

func hashOfReader(c *gc.C, r io.Reader) string {
	h := blobstore.NewHash()
	_, err := io.Copy(h, r)
	c.Assert(err, gc.IsNil)
	return fmt.Sprintf("%x", h.Sum(nil))
}

func hashOf(s string) string {
	h := blobstore.NewHash()
	h.Write([]byte(s))
	return fmt.Sprintf("%x", h.Sum(nil))
}

type dataSource struct {
	buf      []byte
	bufIndex int
	remain   int64
}

// newDataSource returns a stream of size bytes holding
// a repeated number.
func newDataSource(fillWith int64, size int64) io.Reader {
	src := &dataSource{
		remain: size,
	}
	for len(src.buf) < 8*1024 {
		src.buf = strconv.AppendInt(src.buf, fillWith, 10)
		src.buf = append(src.buf, ' ')
	}
	return src
}

func (s *dataSource) Read(buf []byte) (int, error) {
	if int64(len(buf)) > s.remain {
		buf = buf[:int(s.remain)]
	}
	total := len(buf)
	if total == 0 {
		return 0, io.EOF
	}

	for len(buf) > 0 {
		if s.bufIndex == len(s.buf) {
			s.bufIndex = 0
		}
		nb := copy(buf, s.buf[s.bufIndex:])
		s.bufIndex += nb
		buf = buf[nb:]
		s.remain -= int64(nb)
	}
	return total, nil
}
