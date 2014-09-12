package main

import (
	"bytes"
	"crypto/sha512"

	"encoding/base64"
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

	"github.com/juju/loggo"
	"gopkg.in/juju/charm.v3"

	"launchpad.net/lpad"
)

var logger = loggo.GetLogger("charmload_v4")

func main() {
	err := load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

// login to launchpad anonymously using juju Consumer name
// and get all the Branch Tips in the charms Distro.
// For each Branch Tip with name ending in /trunk, publish in
// charmstore
func load() error {
	flags := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	staging := flags.Bool("staging", false, "use the launchpad staging server")
	storeURL := flags.String("storeurl", "http://localhost:8080/v4/", "the URL of the charmstore")
	loggingConfig := flags.String("logging-config", "", "specify log levels for modules e.g. <root>=TRACE")
	showLog := flags.Bool("show-log", false, "if set, write log messages to stderr")
	storeUser := flags.String("user", "admin:example-passwd", "the colon separated user:password for charmstore")
	err := flags.Parse(os.Args[1:])
	if flag.ErrHelp == err {
		flag.Usage()
	}
	server := lpad.Production
	if *staging {
		server = lpad.Staging
	}
	if *loggingConfig != "" {
		loggo.ConfigureLoggers(*loggingConfig)
	}
	if *showLog {
		writer := loggo.NewSimpleWriter(os.Stderr, &loggo.DefaultFormatter{})
		_, err := loggo.ReplaceDefaultWriter(writer)
		if err != nil {
			return err
		}
	}
	oauth := &lpad.OAuth{Anonymous: true, Consumer: "juju"}
	root, err := lpad.Login(server, oauth)
	if err != nil {
		return err
	}

	charmsDistro, err := root.Distro("charms")
	if err != nil {
		return err
	}
	tips, err := charmsDistro.BranchTips(time.Time{})
	if err != nil {
		return err
	}
	for _, tip := range tips {
		if !strings.HasSuffix(tip.UniqueName, "/trunk") {
			continue
		}
		logger.Tracef("getting uniqueNameURLs for %v", tip.UniqueName)
		branchURL, charmURL, err := uniqueNameURLs(tip.UniqueName)
		if err != nil {
			logger.Infof("could not get uniqueNameURLs for %v: %v", tip.UniqueName, err)
			continue
		}
		if tip.Revision == "" {
			logger.Tracef("skipping %v no revision", tip.UniqueName)
			continue
		} else {
			logger.Tracef("found %v with revision %v", tip.UniqueName, tip.Revision)
		}
		URLs := []*charm.URL{charmURL}
		schema, name := charmURL.Schema, charmURL.Name
		addPromulgatedCharmURLs(tip.OfficialSeries, schema, name, URLs)
		err = publishBazaarBranch(*storeURL, *storeUser, URLs, branchURL, tip.Revision)
		if err != nil {
			logger.Errorf("publishing branch %v to charmstore: %v", branchURL, err)
		}
		if _, ok := err.(*UnauthorizedError); ok {
			return err
		}

	}
	return nil
}

// addPromulgatedCharmURLs adds urls from officialSeries to
// the URLs slice for the given schema, name.
// Promulgated charms have OfficialSeries in launchpad.
func addPromulgatedCharmURLs(officialSeries []string, schema, name string, URLs []*charm.URL) {
	for _, series := range officialSeries {
		nextCharmURL := &charm.URL{
			Schema:   schema,
			Name:     name,
			Revision: -1,
			Series:   series,
		}
		URLs = append(URLs, nextCharmURL)
		logger.Debugf("added URL %v to URLs list for %v", nextCharmURL, URLs[0])
	}
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
func uniqueNameURLs(name string) (branchURL string, charmURL *charm.URL, err error) {
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
	charmURL, err = charm.ParseURL(fmt.Sprintf("cs:%s/%s/%s", u[0], u[2], u[3]))
	if err != nil {
		return "", nil, err
	}
	return branchURL, charmURL, nil
}

func publishBazaarBranch(storeURL string, storeUser string, URLs []*charm.URL, branchURL string, digest string) error {
	// Retrieve the branch with a lightweight checkout, so that it
	// builds a working tree as cheaply as possible. History
	// doesn't matter here.
	tempDir, err := ioutil.TempDir("", "publish-branch-")
	if err != nil {
		return err
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
		return err
	}
	if tipDigest != digest {
		digest = tipDigest
		logger.Warningf("tipDigest %v != digest %v", digest, tipDigest)
	}

	logger.Tracef("read CharmDir from branchDir %v", branchDir)
	thisCharm, err := charm.ReadCharmDir(branchDir)
	if err != nil {
		return err
	}
	reader, writer := io.Pipe()
	hash1 := sha512.New384()
	var counter Counter
	mwriter := io.MultiWriter(hash1, &counter)
	thisCharm.ArchiveTo(mwriter)
	hash1str := fmt.Sprintf("%x", hash1.Sum(nil))
	go func() {
		thisCharm.ArchiveTo(writer)
		writer.Close()
	}()
	id := URLs[0]
	URL := storeURL + id.Path() + "/archive?hash=" + hash1str
	logger.Infof("posting to %v", URL)
	request, err := http.NewRequest("POST", URL, reader)
	authhash := base64.StdEncoding.EncodeToString([]byte(storeUser))
	logger.Tracef("encoded Authorization %v", authhash)
	request.Header["Authorization"] = []string{"Basic " + authhash}
	// go1.2.1 has a bug requiring Content-Type to be sent
	// since we are posting to a go server which may be running on
	// 1.2.1, we should send this header
	// https://code.google.com/p/go/source/detail?r=a768c0592b88
	request.Header["Content-Type"] = []string{"application/octet-stream"}
	request.ContentLength = int64(counter)
	resp, err := http.DefaultClient.Do(request)
	if resp.StatusCode == http.StatusUnauthorized {
		logger.Errorf("invalid charmstore credentials")
		return &UnauthorizedError{}
	}
	if err != nil || resp.StatusCode != http.StatusOK {
		logger.Warningf("error posting:", err, resp.Header)
		io.Copy(os.Stdout, resp.Body)
	}
	logger.Tracef("response: %v", resp)

	return err
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

type Counter int

func (c *Counter) Write(p []byte) (n int, err error) {
	size := len(p)
	*c += Counter(size)
	return size, nil
}

type UnauthorizedError struct{}

func (_ *UnauthorizedError) Error() string {
	return "UnauthorizedError"
}
