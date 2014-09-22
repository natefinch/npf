// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package lppublish

import (
	"bytes"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/juju/errgo"
	"github.com/juju/loggo"
	"gopkg.in/juju/charm.v3"
	"launchpad.net/lpad"

	"github.com/juju/charmstore/params"
)

var logger = loggo.GetLogger("charmload")
var failsLogger = loggo.GetLogger("charmload_v4.loadfails")

type PublishBranchError struct {
	URL string
	Err error
}

type PublishBranchErrors []PublishBranchError

func (errs PublishBranchErrors) Error() string {
	return fmt.Sprintf("%d branch(es) failed to be published", len(errs))
}

type Params struct {
	// LaunchpadServer specifies the Launchpad base API URL, such
	// as lpad.Production or lpad.Staging.
	LaunchpadServer lpad.APIBase

	// StoreURL holds the base charm store API URL.
	StoreURL string

	// StoreUser holds the user to authenticate as.
	StoreUser string

	// StoreUser holds the authentication password.
	StorePassword string

	// Limit holds the number of charm/bundles to upload.
	// A zero value means that all the entities are processed.
	Limit int

	// NumPublishers holds the number of publishers that
	// can be run in parallel.
	NumPublishers int
}

type charmLoader Params

// PublishCharmsDistro publishes all branch tips found in
// the /charms distribution in Launchpad onto the
// charm store specified in the given parameter value.
func PublishCharmsDistro(p Params) error {
	cl := (*charmLoader)(&p)
	return cl.run()
}

// run logs in anonymously to Launchpad using the Juju Consumer name,
// gets all the branch tips in the charms Distro and publishes each
// branch tip whose name ends in /trunk.
func (cl *charmLoader) run() error {
	oauth := &lpad.OAuth{Anonymous: true, Consumer: "juju"}
	root, err := lpad.Login(cl.LaunchpadServer, oauth)
	if err != nil {
		return errgo.Notef(err, "cannot log in to launchpad")
	}

	charmsDistro, err := root.Distro("charms")
	if err != nil {
		return errgo.Notef(err, "cannot get charms distro")
	}
	tips, err := charmsDistro.BranchTips(time.Time{})
	if err != nil {
		return errgo.Notef(err, "cannot get branch tips")
	}
	logger.Infof("starting ingestion with %d publisher(s)", cl.NumPublishers)

	// Start retrieving branches to be processed.
	results := make(chan entityResult)
	go func() {
		defer close(results)
		cl.processTips(tips, results)
	}()

	// Start goroutines to publish charm and bundles.
	var wg sync.WaitGroup
	wg.Add(cl.NumPublishers)
	errs := make(chan error)
	for i := 0; i < cl.NumPublishers; i++ {
		go func() {
			cl.publisher(results, errs)
			wg.Done()
		}()
	}
	go func() {
		wg.Wait()
		close(errs)
	}()

	// Wait until the errs channel is closed, and exit immediately if a
	// params.ErrUnauthorized error is encountered.
	for err := range errs {
		if errgo.Cause(err) == params.ErrUnauthorized {
			return errgo.NoteMask(err, "fatal error", errgo.Is(params.ErrUnauthorized))
		}
		logger.Errorf(err.Error())
	}
	return nil
}

// entityResult is the result of processing a charm or bundle branch tip.
type entityResult struct {
	tip       lpad.BranchTip
	branchURL string
	charmURL  *charm.Reference
}

// processTips loops over the given branch tips, and sends an entityResult for
// each valid entity on the results channel.
// It proceeds until all the tips have been processed or the user defined
// limit is reached.
func (cl *charmLoader) processTips(tips []lpad.BranchTip, results chan<- entityResult) {
	counter := 0
	for _, tip := range tips {
		// TODO(jay) need to process bundles as well (there is a card)
		if !strings.HasSuffix(tip.UniqueName, "/trunk") {
			continue
		}
		logger.Tracef("getting uniqueNameURLs for %v", tip.UniqueName)
		branchURL, charmURL, err := uniqueNameURLs(tip.UniqueName)
		if err != nil {
			logger.Warningf("could not get uniqueNameURLs for %v: %v", tip.UniqueName, err)
			continue
		}
		if tip.Revision == "" {
			logger.Errorf("skipping branch %v with no revisions", tip.UniqueName)
			continue
		}
		counter++
		logger.Infof("#%d: found %v with revision %v", counter, tip.UniqueName, tip.Revision)
		results <- entityResult{
			tip:       tip,
			branchURL: branchURL,
			charmURL:  charmURL,
		}
		// If cl.Limit is 0, the check below never succeeds.
		if counter == cl.Limit {
			break
		}
	}
}

// publisher reads the entity results from the given channel and publishes
// the corresponding URLs to the charm store. Errors encountered in the process
// are sent to the given errs channel.
func (cl *charmLoader) publisher(results <-chan entityResult, errs chan<- error) {
	logger.Debugf("starting publisher")
	for result := range results {
		logger.Debugf("start publishing URLs for %s", result.charmURL)
		URLs := []*charm.Reference{result.charmURL}
		URLs = appendPromulgatedCharmURLs(
			result.tip.OfficialSeries,
			result.charmURL.Schema,
			result.charmURL.Name,
			URLs,
		)
		err := cl.publishBazaarBranch(URLs, result.branchURL, result.tip.Revision)
		if err != nil {
			failsLogger.Errorf("cannot publish branch %v to charm store: %v", result.branchURL, err)
			errs <- errgo.NoteMask(err, "cannot publish branch "+result.branchURL, errgo.Is(params.ErrUnauthorized))
		}
		logger.Debugf("done publishing URLs for %s", result.charmURL)
	}
	logger.Debugf("quitting publisher")
}

// appendPromulgatedCharmURLs adds urls from officialSeries to
// the URLs slice for the given schema and name.
// Promulgated charms have OfficialSeries in launchpad.
func appendPromulgatedCharmURLs(officialSeries []string, schema, name string, URLs []*charm.Reference) []*charm.Reference {
	for _, series := range officialSeries {
		nextCharmURL := &charm.Reference{
			Schema:   schema,
			Name:     name,
			Revision: -1,
			Series:   series,
		}
		URLs = append(URLs, nextCharmURL)
		logger.Debugf("added URL %v to URLs list for %v", nextCharmURL, URLs[0])
	}
	return URLs
}

// uniqueNameURLs returns the branch URL and the charm URL for the
// provided Launchpad branch unique name. The unique name must be
// in the form:
//
//     ~<user>/charms/<series>/<charm name>/trunk
//
// For testing purposes, if name has a prefix preceding a string in
// this format, the prefix is stripped out for computing the charm
// URL, and the unique name is returned unchanged as the branch URL.
func uniqueNameURLs(name string) (branchURL string, charmURL *charm.Reference, err error) {
	u := strings.Split(name, "/")
	if len(u) > 5 {
		u = u[len(u)-5:]
		branchURL = name
	} else {
		branchURL = "lp:" + name
	}
	if len(u) < 5 || u[1] != "charms" || u[4] != "trunk" || len(u[0]) == 0 || u[0][0] != '~' {
		return "", nil, fmt.Errorf("unsupported branch name: %s", name)
	}
	charmURL, err = charm.ParseReference(fmt.Sprintf("cs:%s/%s/%s", u[0], u[2], u[3]))
	if err != nil {
		return "", nil, errgo.Mask(err)
	}
	return branchURL, charmURL, nil
}

const bzrDigestKey = "bzr-digest"

func (cl *charmLoader) publishBazaarBranch(URLs []*charm.Reference, branchURL string, digest string) error {
	// Check whether the entity is already present in the charm store.
	missingURLs := make([]*charm.Reference, 0, len(URLs))
	for _, id := range URLs {
		existingDigest, err := cl.getDigestExtraInfo(id)
		if err == nil && existingDigest == digest {
			logger.Infof("skipping %v: entity already present in the charm store with digest %v", id, digest)
			continue
		}
		if err != nil && errgo.Cause(err) != params.ErrNotFound {
			logger.Warningf("problem retrieving extra info for %v: %v", id, err)
		}
		missingURLs = append(missingURLs, id)
	}
	if len(missingURLs) == 0 {
		logger.Debugf("nothing to do for %s", branchURL)
		return nil
	}

	// Retrieve the branch with a lightweight checkout, so that it
	// builds a working tree as cheaply as possible. History
	// doesn't matter here.
	tempDir, err := ioutil.TempDir("", "publish-branch-")
	if err != nil {
		return errgo.Notef(err, "cannot make temp dir")
	}
	defer os.RemoveAll(tempDir)
	branchDir := filepath.Join(tempDir, "branch")
	logger.Debugf("running bzr checkout ... %v", branchURL)
	output, err := exec.Command("bzr", "checkout", "--lightweight", branchURL, branchDir).CombinedOutput()
	if err != nil {
		return outputErr(output, err)
	}

	// Retrieve the Bazaar digest of the branch.
	tipDigest, err := bzrRevisionId(branchDir)
	if err != nil {
		return errgo.Notef(err, "cannot get revision id")
	}
	if tipDigest != digest {
		digest = tipDigest
		logger.Warningf("tipDigest %v != digest %v", digest, tipDigest)
	}

	// Instantiate the entity from the branch directory.
	charmDir, err := charm.ReadCharmDir(branchDir)
	if err != nil {
		return errgo.Notef(err, "cannot read charm dir")
	}

	// Archive the entity in a temporary directory, and calculate its SHA384
	// hash and archive size.
	tempFile, hash, archiveSize, err := cl.archiveCharmDir(charmDir, tempDir)
	if err != nil {
		return errgo.Notef(err, "cannot make archive")
	}
	defer tempFile.Close()

	// Publish the entity to the corresponding URLs in the charm store.
	for _, id := range missingURLs {
		if _, err := tempFile.Seek(0, 0); err != nil {
			return errgo.Notef(err, "cannot seek")
		}
		finalId, err := cl.postArchive(tempFile, id, archiveSize, hash)
		if err != nil {
			return errgo.NoteMask(err, "cannot post archive", errgo.Is(params.ErrUnauthorized))
		}
		logger.Infof("posted %s", finalId)

		// Set the Bazaar digest as extra-info for the entity.
		if err := cl.putDigestExtraInfo(finalId, tipDigest); err != nil {
			return errgo.Notef(err, "cannot add digest extra info")
		}
		logger.Infof("extra info pushed for %s", finalId)
	}
	return err
}

// archiveCharmDir archives the charmDir to a temporary file
// inside tempDir and returns the file, its hash and size.
func (cl *charmLoader) archiveCharmDir(charmDir *charm.CharmDir, tempDir string) (archiveFile *os.File, hash string, size int64, err error) {
	f, err := os.Create(filepath.Join(tempDir, "archive.zip"))
	if err != nil {
		return nil, "", 0, errgo.Notef(err, "cannot create temp file")
	}
	logger.Debugf("writing charm to temporary file %s", f.Name())
	if err != nil {
		return nil, "", 0, errgo.Notef(err, "cannot make temp file")
	}
	defer func() {
		if err != nil {
			f.Close()
		}
	}()
	sha384 := sha512.New384()
	err = charmDir.ArchiveTo(io.MultiWriter(f, sha384))
	if err != nil {
		return nil, "", 0, errgo.Notef(err, "cannot archive charm")
	}
	fileInfo, err := f.Stat()
	if err != nil {
		return nil, "", 0, errgo.Notef(err, "cannot stat temporary file")
	}
	hash = fmt.Sprintf("%x", sha384.Sum(nil))
	return f, hash, fileInfo.Size(), nil
}

func (cl *charmLoader) postArchive(r io.Reader, id *charm.Reference, size int64, hash string) (*charm.Reference, error) {
	url := cl.StoreURL + id.Path() + "/archive?hash=" + hash
	logger.Infof("posting to %v", url)
	// Note that http.Request.Do closes the body if implements
	// io.Closer. This is unwarranted familiarity and we don't want
	// it, so wrap the reader to prevent it happening.
	req, err := http.NewRequest("POST", url, ioutil.NopCloser(r))
	if err != nil {
		return nil, errgo.Notef(err, "cannot make HTTP POST request")
	}
	req.Header.Set("Content-Type", "application/zip")
	req.ContentLength = size

	var resp params.ArchivePostResponse
	err = cl.doCharmStoreRequest(req, &resp)
	if err != nil {
		return nil, errgo.Mask(err, errgo.Is(params.ErrUnauthorized))
	}
	return resp.Id, nil
}

func (cl *charmLoader) getDigestExtraInfo(id *charm.Reference) (string, error) {
	url := cl.StoreURL + id.Path() + "/meta/extra-info/" + bzrDigestKey
	logger.Infof("getting extra info from %v", url)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", errgo.Notef(err, "cannot make HTTP GET request")
	}

	var resp string
	err = cl.doCharmStoreRequest(req, &resp)
	if err != nil {
		return "", errgo.Mask(err, errgo.Is(params.ErrNotFound))
	}
	return resp, nil
}

func (cl *charmLoader) putDigestExtraInfo(id *charm.Reference, digest string) error {
	body, err := json.Marshal(digest)
	if err != nil {
		return errgo.Notef(err, "cannot marshal digest")
	}
	url := cl.StoreURL + id.Path() + "/meta/extra-info/" + bzrDigestKey
	logger.Infof("putting extra info to %v", url)
	req, err := http.NewRequest("PUT", url, bytes.NewReader(body))
	if err != nil {
		return errgo.Notef(err, "cannot make HTTP PUT request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = int64(len(body))

	err = cl.doCharmStoreRequest(req, nil)
	if err != nil {
		return errgo.Mask(err, errgo.Is(params.ErrUnauthorized))
	}
	return nil
}

// doCharmStoreRequest adds appropriate headers to the given HTTP request,
// sends it to the charm store acting as the given user
// and parses the result as JSON into the given result value,
// which should be a pointer to the expected data.
// TODO(rog) factor this into a general charm store client package.
func (cl *charmLoader) doCharmStoreRequest(req *http.Request, result interface{}) error {
	if req.Method == "POST" || req.Method == "PUT" {
		userPass := cl.StoreUser + ":" + cl.StorePassword
		authBasic := base64.StdEncoding.EncodeToString([]byte(userPass))
		req.Header.Set("Authorization", "Basic "+authBasic)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return errgo.Mask(err)
	}

	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	if resp.StatusCode != http.StatusOK {
		var perr params.Error
		if err := dec.Decode(&perr); err != nil {
			return errgo.Notef(err, "cannot unmarshal error response")
		}
		if perr.Message == "" {
			return errgo.New("error response with empty message")
		}
		return &perr
	}
	// Check if the caller is interested in the successful server response.
	if result == nil {
		return nil
	}
	if err := dec.Decode(result); err != nil {
		// TODO(rog) return a more informative error in this case
		// (we might actually be talking to a proxy, which may
		// return any sort of error)
		return errgo.Notef(err, "cannot unmarshal response")
	}
	return nil
}

// bzrRevisionId returns the Bazaar revision id for the branch in branchDir.
func bzrRevisionId(branchDir string) (string, error) {
	cmd := exec.Command("bzr", "revision-info")
	cmd.Dir = branchDir
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	output, err := cmd.Output()
	if err != nil {
		output = append(output, '\n')
		output = append(output, stderr.Bytes()...)
		return "", outputErr(output, err)
	}
	pair := bytes.Fields(output)
	if len(pair) != 2 {
		output = append(output, '\n')
		output = append(output, stderr.Bytes()...)
		return "", fmt.Errorf(`invalid output from "bzr revision-info": %s`, output)
	}
	return string(pair[1]), nil
}

// outputErr returns an error that assembles some command's output and its
// error, if both output and err are set, and returns only err if output is nil.
func outputErr(output []byte, err error) error {
	if len(output) > 0 {
		return fmt.Errorf("%v\n%s", err, output)
	}
	return err
}
