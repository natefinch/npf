package blobstore

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"strconv"

	"labix.org/v2/mgo"

	"github.com/juju/blobstore"
	"github.com/juju/charmstore/internal/multihash"
	"github.com/juju/errors"
)

type ReadSeekCloser interface {
	io.Reader
	io.Seeker
	io.Closer
}

// ContentChallengeError holds a proof-of-content
// challenge produced by a blobstore.
type ContentChallengeError struct {
	Req ContentChallenge
}

func (e *ContentChallengeError) Error() string {
	return "cannot upload because proof of content ownership is required"
}

// ContentChallenge holds a proof-of-content challenge
// produced by a blobstore. A client can satisfy the request
// by producing a ContentChallengeResponse containing
// the same request id and a hash of RangeLength bytes
// of the content starting at RangeStart.
type ContentChallenge struct {
	RequestId   string
	RangeStart  int64
	RangeLength int64
}

// ContentChallengeResponse holds a response to a ContentChallenge.
type ContentChallengeResponse struct {
	RequestId string
	Hash      string
}

// NewHash is used to calculate checksums for the blob store.
func NewHash() hash.Hash {
	return multihash.New(md5.New(), sha256.New())
}

// NewContentChallengeResponse can be used by a client to respond to a content
// challenge. The returned value should be passed to BlobStorage.Put
// when the client retries the request.
func NewContentChallengeResponse(chal *ContentChallenge, r io.ReadSeeker) (*ContentChallengeResponse, error) {
	_, err := r.Seek(chal.RangeStart, 0)
	if err != nil {
		return nil, err
	}
	hash := NewHash()
	nw, err := io.CopyN(hash, r, chal.RangeLength)
	if err != nil {
		return nil, err
	}
	if nw != chal.RangeLength {
		return nil, fmt.Errorf("content is not long enough")
	}
	return &ContentChallengeResponse{
		RequestId: chal.RequestId,
		Hash:      fmt.Sprintf("%x", hash.Sum(nil)),
	}, nil
}

// Store stores data blobs in mongodb, de-duplicating by
// blob hash.
type Store struct {
	mstore blobstore.ManagedStorage
}

// New returns a new blob store that writes to the given database,
// prefixing its collections with the given prefix.
func New(db *mgo.Database, prefix string) *Store {
	rs := blobstore.NewGridFS(db.Name, prefix, db.Session)
	return &Store{
		mstore: blobstore.NewManagedStorage(db, rs),
	}
}

func (s *Store) challengeResponse(resp *ContentChallengeResponse) error {
	id, err := strconv.ParseInt(resp.RequestId, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid request id %q", id)
	}
	rh := newResourceHash(resp.Hash)
	return s.mstore.ProofOfAccessResponse(blobstore.NewPutResponse(id, rh.MD5Hash, rh.SHA256Hash))
}

// Put tries to stream the content from the given reader into blob
// storage. The content should have the given size and hash. If the
// content is already in the store, a ContentChallengeError is returned
// containing a challenge that must be satisfied by a client to prove
// that they have access to the content. If the proof has already been
// acquired, it should be passed in as the proof argument.
func (s *Store) Put(r io.Reader, size int64, hash string, proof *ContentChallengeResponse) (*ContentChallenge, error) {
	if proof != nil {
		err := s.challengeResponse(proof)
		if err == nil {
			return nil, nil
		}
		if err != blobstore.ErrResourceDeleted {
			return nil, err
		}
		// The blob has been deleted since the challenge
		// was created, so continue on with uploading
		// the content as if there was no previous challenge.
	}
	resp, err := s.mstore.PutForEnvironmentRequest("", hash, *newResourceHash(hash))
	if err != nil {
		if errors.IsNotFound(err) {
			if err := s.mstore.PutForEnvironment("", hash, r, size); err != nil {
				return nil, err
			}
			return nil, nil
		}
		return nil, err
	}
	return &ContentChallenge{
		RequestId:   fmt.Sprint(resp.RequestId),
		RangeStart:  resp.RangeStart,
		RangeLength: resp.RangeLength,
	}, nil
}

// Open opens the blob with the given hash.
func (s *Store) Open(hashSum string) (ReadSeekCloser, int64, error) {
	r, length, err := s.mstore.GetForEnvironment("", hashSum)
	if err != nil {
		return nil, 0, err
	}
	return r.(ReadSeekCloser), length, nil
}

// newResourceHash returns a ResourceHash equivalent to the
// given hashSum. It does not complain if hashSum is invalid - the
// lower levels will fail appropriately.
func newResourceHash(hashSum string) *blobstore.ResourceHash {
	p := hex.EncodedLen(md5.Size)
	if len(hashSum) < p {
		return &blobstore.ResourceHash{
			MD5Hash: hashSum,
		}
	}
	return &blobstore.ResourceHash{
		MD5Hash:    hashSum[0:p],
		SHA256Hash: hashSum[p:],
	}
}
