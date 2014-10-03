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
	"github.com/juju/utils/fs"
	"gopkg.in/juju/charm.v4"
	"gopkg.in/juju/charm.v4/migratebundle"
	"gopkg.in/yaml.v1"
	"launchpad.net/lpad"

	"github.com/juju/charmstore/params"
)

// BzrDigestKey is the extra-info key used to store the Bazaar digest
// of an entity ingested from Launchpad.
const BzrDigestKey = "bzr-digest"

var logger = loggo.GetLogger("charmload")
var failsLogger = loggo.GetLogger("charmload_v4.loadfails")

const officialBundlePromulgator = "charmers"

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

	// totalCount holds the number of charms or bundles
	// processed so far.
	totalCount int
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

	// Publish all charms before bundles so that bundles that
	// rely on the charms can verify correctly when uploaded.
	charms, bundles := splitBundleTips(tips)
	if err := cl.publishTips(charms); err != nil {
		return errgo.Mask(err, errgo.Is(params.ErrUnauthorized))
	}
	logger.Infof("published all charms; now publishing bundles")
	if err := cl.publishTips(bundles); err != nil {
		return errgo.Mask(err, errgo.Is(params.ErrUnauthorized))
	}
	return nil
}

func (cl *charmLoader) publishTips(tips []lpad.BranchTip) error {
	// Start retrieving branches to be processed.
	results := make(chan entityResult)
	go func() {
		defer close(results)
		cl.processTips(tips, results)
	}()
	logger.Infof("starting ingestion with %d publisher(s)", cl.NumPublishers)

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

func splitBundleTips(tips []lpad.BranchTip) (charms, bundles []lpad.BranchTip) {
	lastBundle := len(tips)
	for i := len(tips) - 1; i >= 0; i-- {
		if strings.HasSuffix(tips[i].UniqueName, "/bundle") {
			tips[i], tips[lastBundle-1] = tips[lastBundle-1], tips[i]
			lastBundle--
		}
	}
	return tips[0:lastBundle], tips[lastBundle:]
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
	for _, tip := range tips {
		if !strings.HasSuffix(tip.UniqueName, "/trunk") && !strings.HasSuffix(tip.UniqueName, "/bundle") {
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
		logger.Debugf("#%d: found %v with revision %v", cl.totalCount, tip.UniqueName, tip.Revision)
		results <- entityResult{
			tip:       tip,
			branchURL: branchURL,
			charmURL:  charmURL,
		}
		// If cl.Limit is 0, the check below never succeeds.
		if cl.totalCount++; cl.totalCount == cl.Limit {
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
		urls := []*charm.Reference{result.charmURL}
		urls = append(urls, promulgatedURLs(result.charmURL, result.tip.OfficialSeries)...)
		err := cl.publishBazaarBranch(urls, result.branchURL, result.tip.Revision)
		if err != nil {
			failsLogger.Errorf("cannot publish branch %v to charm store: %v", result.branchURL, err)
			errs <- errgo.NoteMask(err, "cannot publish branch "+result.branchURL, errgo.Is(params.ErrUnauthorized))
		}
		logger.Debugf("done publishing URLs for %s", result.charmURL)
	}
	logger.Debugf("quitting publisher")
}

// promulgatedURLs returns any URLs that the given url should
// also be published to.
func promulgatedURLs(url *charm.Reference, officialSeries []string) []*charm.Reference {
	// The only way we know if bundles are promulgated is if they
	// are published by the official user.
	if url.Series == "bundle" && url.User == officialBundlePromulgator {
		return []*charm.Reference{{
			Schema:   url.Schema,
			Name:     url.Name,
			Revision: -1,
			Series:   "bundle",
		}}
	}
	// Promulgated charms have OfficialSeries in launchpad.
	urls := make([]*charm.Reference, len(officialSeries))
	for i, series := range officialSeries {
		urls[i] = &charm.Reference{
			Schema:   url.Schema,
			Name:     url.Name,
			Revision: -1,
			Series:   series,
		}
		logger.Debugf("added URL %v to URLs list for %v", urls[i], urls[0])
	}
	return urls
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
	if notSupportedBranchName(u) {
		return "", nil, fmt.Errorf("unsupported branch name: %s", name)
	}
	charmURL, err = charm.ParseReference(fmt.Sprintf("cs:%s/%s/%s", u[0], u[2], u[3]))
	if err != nil {
		return "", nil, errgo.Mask(err)
	}
	// The charm store uses "bundle" for the series of a bundle, not "bundles".
	if charmURL.Series == "bundles" {
		charmURL.Series = "bundle"
	}
	return branchURL, charmURL, nil
}

func notSupportedBranchName(u []string) bool {
	if len(u) < 5 || u[1] != "charms" || (u[4] != "trunk" && u[4] != "bundle") || len(u[0]) == 0 || u[0][0] != '~' {
		return true
	}
	return false
}

const bzrDigestKey = "bzr-digest"

func (cl *charmLoader) publishBazaarBranch(urls []*charm.Reference, branchURL string, digest string) error {
	// Check whether the entity is already present in the charm store.
	urls = cl.excludeExistingEntities(urls, digest)
	if len(urls) == 0 {
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
	if urls[0].Series != "bundle" {
		// Instantiate the charm from the branch directory.
		ch, err := charm.ReadCharmDir(branchDir)
		if err != nil {
			return errgo.Notef(err, "cannot read charm dir")
		}
		if err := cl.publish(urls, ch, tempDir, tipDigest); err != nil {
			return errgo.Notef(err, "cannot publish")
		}
		return nil
	}
	bundles, err := readBundles(branchDir, tempDir)
	if err != nil {
		return errgo.Notef(err, "cannot read bundle %q", urls[0])
	}
	// Publish each bundle at a different path. If there was only
	// one bundle found, it's either a new style bundle or a legacy
	// basket with only one bundle in. In either of those cases, we
	// publish to the original URLs. When there's more than one
	// bundle, we append the bundle name to the URLs prefixed with a
	// hyphen.
	for name, bundle := range bundles {
		var finalURLs []*charm.Reference
		if len(bundles) == 1 {
			finalURLs = urls
		} else {
			finalURLs = make([]*charm.Reference, len(urls))
			for i, url := range urls {
				finalURLs[i] = new(charm.Reference)
				*finalURLs[i] = *url
				finalURLs[i].Name += "-" + name
			}
		}
		if err := cl.publish(finalURLs, bundle, tempDir, tipDigest); err != nil {
			return errgo.Notef(err, "cannot publish %q", name)
		}
	}
	return nil
}

// readBundles reads the bundle or legacy "basket"
// within the given directory and returns a map with one
// entry for each bundle found, keyed by
// bundle name.
func readBundles(dir string, tempDir string) (map[string]*charm.BundleDir, error) {
	// If there's a bundle.yaml file, it's a new-style bundle,
	// so no need for migration.
	if _, err := os.Stat(filepath.Join(dir, "bundle.yaml")); err == nil {
		b, err := charm.ReadBundleDir(dir)
		if err != nil {
			return nil, errgo.Mask(err)
		}
		return map[string]*charm.BundleDir{"": b}, nil
	}
	f, err := os.Open(filepath.Join(dir, "bundles.yaml"))
	if err != nil {
		return nil, errgo.Mask(err)
	}
	data, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, errgo.Notef(err, "cannot read bundles.yaml")
	}
	bds, err := migratebundle.Migrate(data, func(id *charm.Reference) (*charm.Meta, error) {
		return nil, errgo.Newf("charm %q not found (not implemented)", id)
	})
	if err != nil {
		return nil, errgo.Notef(err, "cannot migrate")
	}
	bundles := make(map[string]*charm.BundleDir)
	i := 0
	for name, bd := range bds {
		destDir := filepath.Join(tempDir, fmt.Sprintf("branch-%d", i))
		i++
		// Copy the whole directory so that we don't have
		// to second-guess the charm package's choice
		// of README file name (or any other files that may
		// happen to be around).
		if err := fs.Copy(dir, destDir); err != nil {
			return nil, errgo.Notef(err, "cannot copy bundle directory")
		}
		if err := os.Rename(filepath.Join(destDir, "bundles.yaml"), filepath.Join(destDir, "bundles.yaml.orig")); err != nil {
			return nil, errgo.Mask(err)
		}
		data, err := yaml.Marshal(bd)
		if err != nil {
			return nil, errgo.Notef(err, "cannot marshal bundle %q", name)
		}
		if err := ioutil.WriteFile(filepath.Join(destDir, "bundle.yaml"), data, 0666); err != nil {
			return nil, errgo.Mask(err)
		}
		bundle, err := charm.ReadBundleDir(destDir)
		if err != nil {
			return nil, errgo.Notef(err, "cannot read bundle %q", name)
		}
		bundles[name] = bundle
	}
	return bundles, nil
}

// publish publishes the given charm or bundle held in archiveDir
// to the charm store with the given URLs, using tempDir
// for temporary storage. The tipDigest parameter holds
// the digest of the launchpad tip holding the charm or bundle.
func (cl *charmLoader) publish(urls []*charm.Reference, archiveDir archiverTo, tempDir string, tipDigest string) error {
	// Archive the entity in a temporary directory, and calculate its SHA384
	// hash and archive size.
	tempFile, hash, archiveSize, err := cl.archiveDir(archiveDir, tempDir)
	if err != nil {
		return errgo.Notef(err, "cannot make archive")
	}
	defer tempFile.Close()

	// Publish the entity to the corresponding URLs in the charm store.
	for _, id := range urls {
		if _, err := tempFile.Seek(0, 0); err != nil {
			return errgo.Notef(err, "cannot seek")
		}
		finalId, err := cl.postArchive(tempFile, id, archiveSize, hash)
		if err != nil {
			return errgo.NoteMask(err, "cannot post archive", errgo.Is(params.ErrUnauthorized))
		}
		logger.Infof("posted %s", finalId)

		// Set the Bazaar digest as extra-info for the entity.
		path := finalId.Path() + "/meta/extra-info/" + BzrDigestKey
		if err := cl.charmStorePut(path, tipDigest); err != nil {
			return errgo.Notef(err, "cannot add digest extra info")
		}
		logger.Infof("bzr digest for %s set to %s", finalId, tipDigest)
	}
	return nil
}

// excludeExistingEntities filters the given URLs slice to only include
// entities that are not already present in the charm store.
// Note that this will not work when the final URLs don't match
// the given URLs. This happens when a multi-bundle legacy
// "basket" has been published.
func (cl *charmLoader) excludeExistingEntities(urls []*charm.Reference, digest string) []*charm.Reference {
	missing := make([]*charm.Reference, 0, len(urls))
	for _, id := range urls {
		var resp string
		path := id.Path() + "/meta/extra-info/" + BzrDigestKey
		err := cl.charmStoreGet(path, &resp)
		if err == nil && resp == digest {
			logger.Infof("skipping %v: entity already present in the charm store with digest %v", id, digest)
			continue
		}
		if err != nil && errgo.Cause(err) != params.ErrNotFound {
			logger.Warningf("problem retrieving extra info for %v: %v", id, err)
		}
		missing = append(missing, id)
	}
	return missing
}

type archiverTo interface {
	ArchiveTo(io.Writer) error
}

// archiveDir archives the archiver to a temporary file
// inside tempDir and returns the file, its hash and size.
func (cl *charmLoader) archiveDir(archiver archiverTo, tempDir string) (archiveFile *os.File, hash string, size int64, err error) {
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
	err = archiver.ArchiveTo(io.MultiWriter(f, sha384))
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
	logger.Infof("sending POST request to %v", url)
	// Note that http.Request.Do closes the body if implements
	// io.Closer. This is unwarranted familiarity and we don't want
	// it, so wrap the reader to prevent it happening.
	req, err := http.NewRequest("POST", url, ioutil.NopCloser(r))
	if err != nil {
		return nil, errgo.Notef(err, "cannot make HTTP POST request")
	}
	req.Header.Set("Content-Type", "application/zip")
	req.ContentLength = size

	var resp params.ArchiveUploadResponse
	err = cl.doCharmStoreRequest(req, &resp)
	if err != nil {
		return nil, errgo.Mask(err, errgo.Is(params.ErrUnauthorized))
	}
	return resp.Id, nil
}

// charmStorePut makes a GET request to the given URL path in
// the charm store and parses the result as JSON into the given
// resp value, which should be a pointer to the expected data.
func (cl *charmLoader) charmStoreGet(path string, resp interface{}) error {
	url := cl.StoreURL + path
	logger.Infof("sending GET request to %v", url)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return errgo.Notef(err, "cannot make HTTP GET request")
	}

	if err := cl.doCharmStoreRequest(req, resp); err != nil {
		return errgo.Mask(err, errgo.Is(params.ErrNotFound))
	}
	return nil
}

// charmStorePut makes a PUT request to the given URL path in
// the charm store with the given body.
func (cl *charmLoader) charmStorePut(path string, body interface{}) error {
	content, err := json.Marshal(body)
	if err != nil {
		return errgo.Notef(err, "cannot marshal body")
	}

	url := cl.StoreURL + path
	logger.Infof("sending PUT request to %v", url)
	req, err := http.NewRequest("PUT", url, bytes.NewReader(content))
	if err != nil {
		return errgo.Notef(err, "cannot make HTTP PUT request")
	}
	req.Header.Set("Content-Type", "application/json")

	if err := cl.doCharmStoreRequest(req, nil); err != nil {
		return errgo.Mask(err, errgo.Is(params.ErrUnauthorized))
	}
	return nil
}

// doCharmStoreRequest adds appropriate headers to the given HTTP request,
// sends it to the charm store acting as the given user and parses the result
// as JSON into the given result value, which should be a pointer to the
// expected data, but may be nil if no result is expected.
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
