package main

import (
	"bytes"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/juju/errgo"
	"github.com/juju/loggo"
	"gopkg.in/juju/charm.v3"
	"launchpad.net/lpad"

	"github.com/juju/charmstore/config"
	"github.com/juju/charmstore/params"
)

var logger = loggo.GetLogger("charmload")

var (
	staging       = flag.Bool("staging", false, "use the launchpad staging server")
	storeAddr     = flag.String("storeaddr", "localhost:8080", "the address of the charmstore; overrides configuration file")
	loggingConfig = flag.String("logging-config", "", "specify log levels for modules e.g. <root>=TRACE")
	storeUser     = flag.String("u", "", "the colon separated user:password for charmstore; overrides configuration file")
	configPath    = flag.String("config", "", "path to charm store configuration file")
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: charmload [flags]\n")
		flag.PrintDefaults()
		os.Exit(2)
	}
	flag.Parse()
	if err := load(); err != nil {
		fmt.Fprintf(os.Stderr, "charmload: %v\n", err)
		os.Exit(1)
	}
}

func load() error {
	if *loggingConfig != "" {
		if err := loggo.ConfigureLoggers(*loggingConfig); err != nil {
			return errgo.Notef(err, "cannot configure loggers")
		}
	}
	var cl charmLoader

	cl.launchpadServer = lpad.Production
	if *staging {
		cl.launchpadServer = lpad.Staging
	}
	var cfg *config.Config
	if *configPath != "" {
		var err error
		cfg, err = config.Read(*configPath)
		if err != nil {
			return errgo.Notef(err, "cannot read config file")
		}
		logger.Infof("config: %#v", cfg)
	}
	if *storeUser != "" {
		parts := strings.SplitN(*storeUser, ":", 2)
		if len(parts) != 2 || len(parts[0]) == 0 {
			return errgo.Newf("invalid user name:password %q", *storeUser)
		}
		cl.storeUser, cl.storePassword = parts[0], parts[1]
	} else if cfg != nil {
		cl.storeUser, cl.storePassword = cfg.AuthUsername, cfg.AuthPassword
	}
	if *storeAddr == "" {
		*storeAddr = cfg.APIAddr
	}
	cl.storeURL = "http://" + *storeAddr + "/v4/"

	if err := cl.run(); err != nil {
		return errgo.Mask(err)
	}
	return nil
}

type charmLoader struct {
	launchpadServer lpad.APIBase
	storeURL        string
	storeUser       string
	storePassword   string
}

// run logs in anonymously to launchpad using the juju Consumer name,
// gets all the branch tips in the charms Distro and publishes each
// branch tip whose name ends in /trunk.
func (cl *charmLoader) run() error {
	oauth := &lpad.OAuth{Anonymous: true, Consumer: "juju"}
	root, err := lpad.Login(cl.launchpadServer, oauth)
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
		logger.Infof("found %v with revision %v", tip.UniqueName, tip.Revision)
		URLs := []*charm.Reference{charmURL}
		URLs = appendPromulgatedCharmURLs(
			tip.OfficialSeries,
			charmURL.Schema,
			charmURL.Name,
			URLs,
		)
		err = cl.publishBazaarBranch(URLs, branchURL, tip.Revision)
		if err != nil {
			logger.Errorf("cannot publish branch %v to charm store: %v", branchURL, err)
		}
		if errgo.Cause(err) == params.ErrUnauthorized {
			return errgo.NoteMask(err, "fatal error", errgo.Is(params.ErrUnauthorized))
		}
	}
	return nil
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

func (cl *charmLoader) publishBazaarBranch(URLs []*charm.Reference, branchURL string, digest string) error {
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

	tipDigest, err := bzrRevisionId(branchDir)
	if err != nil {
		return errgo.Notef(err, "cannot get revision id")
	}
	if tipDigest != digest {
		digest = tipDigest
		logger.Warningf("tipDigest %v != digest %v", digest, tipDigest)
	}
	charmDir, err := charm.ReadCharmDir(branchDir)
	if err != nil {
		return errgo.Notef(err, "cannot read charm dir")
	}
	tempFile, hash, archiveSize, err := cl.archiveCharmDir(charmDir, tempDir)
	if err != nil {
		return errgo.Notef(err, "cannot make archive")
	}
	defer tempFile.Close()
	for _, id := range URLs {
		if _, err := tempFile.Seek(0, 0); err != nil {
			return errgo.Notef(err, "cannot seek")
		}
		finalId, err := cl.postArchive(tempFile, id, archiveSize, hash)
		if err != nil {
			return errgo.NoteMask(err, "cannot post archive", errgo.Is(params.ErrUnauthorized))
		}
		logger.Infof("posted %s", finalId)
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
	URL := cl.storeURL + id.Path() + "/archive?hash=" + hash
	logger.Infof("posting to %v", URL)
	req, err := http.NewRequest("POST", URL, r)
	if err != nil {
		return nil, errgo.Notef(err, "cannot make http request")
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

// doCharmStoreRequest adds appropriate headers to the given HTTP request,
// sends it to the charm store acting as the given user
//  and parses the result as JSON into the
// given result value, which should be a pointer to the expected data.
// TODO(rog) factor this into a general charm store client package.
func (cl *charmLoader) doCharmStoreRequest(req *http.Request, result interface{}) error {
	userPass := cl.storeUser + ":" + cl.storePassword
	authBasic := base64.StdEncoding.EncodeToString([]byte(userPass))
	req.Header.Set("Authorization", "Basic "+authBasic)

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
	if err := dec.Decode(result); err != nil {
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
