package charmstore

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"go/build"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	jujutesting "github.com/juju/testing"
	"github.com/juju/utils/fs"
	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/macaroon-bakery.v1/bakery"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
	"gopkg.in/tomb.v2"
	"gopkg.in/yaml.v2"

	"gopkg.in/juju/charmstore.v5-unstable/config"
	"gopkg.in/juju/charmstore.v5-unstable/internal/blobstore"
	"gopkg.in/juju/charmstore.v5-unstable/internal/router"
)

// historicalDBName holds the name of the juju database
// as hard-coded in previous versions of the charm store server.
const historicalDBName = "juju"

// dumpMigrationDatabase checks out and runs the charmstore version held
// in each element of history in sequence, runs any associated updates,
// and, if the version is not before earlierDeployedVersion, dumps
// the database to a file.
//
// After dumpMigrationDatabase has been called, createDatabaseAtVersion
// can be used to backtrack the database to any of the dumped versions.
func dumpMigrationHistory(session *mgo.Session, earliestDeployedVersion string, history []versionSpec) error {
	db := session.DB(historicalDBName)
	vcsStatus, err := currentVCSStatus()
	if err != nil {
		return errgo.Mask(err)
	}
	dumping := false
	for _, vc := range history {
		logger.Infof("----------------- running version %v", vc.version)
		if vc.version == earliestDeployedVersion {
			dumping = true
		}
		if err := runMigrationVersion(db, vc); err != nil {
			return errgo.Notef(err, "cannot run at version %s", vc.version)
		}
		if dumping {
			filename := migrationDumpFileName(vc.version)
			logger.Infof("dumping database to %s", filename)
			if err := saveDBToFile(db, vcsStatus, filename); err != nil {
				return errgo.Notef(err, "cannot save DB at version %v", vc.version)
			}
		}
	}
	if !dumping {
		return errgo.Newf("no versions matched earliest deployed version %q; nothing dumped", earliestDeployedVersion)
	}
	return nil
}

// createDatabaseAtVersion changes the state of the
// database to be as it is after running the database
// migrations from all the versions up to and including
// the given version, which must be a version in the
// dumped history not before the earliest deployed version.
//
// It finds the database snapshot from the appropriate
// dump file (see dumpMigrationHistory).
func createDatabaseAtVersion(db *mgo.Database, version string) error {
	vcsStatus, err := restoreDBFromFile(db, migrationDumpFileName(version))
	if err != nil {
		return errgo.Notef(err, "cannot restore version %q", version)
	}
	logger.Infof("restored migration from version %s; dumped at %s", version, vcsStatus)
	return nil
}

// migrationDumpFileName returns the name of the file that
// the migration database snapshot will be saved to.
func migrationDumpFileName(version string) string {
	return "migrationdump." + version + ".zip"
}

// currentVCSStatus returns the git status of the current
// charmstore source code. This will be saved into the
// migration dump file so that there is some indication
// as to when that was created.
func currentVCSStatus() (string, error) {
	cmd := exec.Command("git", "describe")
	cmd.Stderr = os.Stderr
	data, err := cmd.Output()
	if err != nil {
		return "", errgo.Mask(err)
	}
	// With the --porcelain flag, git status prints a simple
	// line-per-locally-modified-file, or nothing at all if there
	// are no locally modified files.
	cmd = exec.Command("git", "status", "--porcelain")
	cmd.Stderr = os.Stderr
	data1, err := cmd.Output()
	if err != nil {
		return "", errgo.Mask(err)
	}
	return string(append(data, data1...)), nil
}

// saveDBToFile dumps the entire state of the database to the given
// file name, also saving the given VCS status.
func saveDBToFile(db *mgo.Database, vcsStatus string, filename string) (err error) {
	f, err := os.Create(filename)
	if err != nil {
		return errgo.Mask(err)
	}
	defer func() {
		if err != nil {
			os.Remove(filename)
		}
	}()
	defer f.Close()
	zw := zip.NewWriter(f)
	defer func() {
		if err1 := zw.Close(); err1 != nil {
			err = errgo.Notef(err1, "zip close failed")
		}
	}()
	collections, err := dumpDB(db)
	if err != nil {
		return errgo.Mask(err)
	}
	if err := writeVCSStatus(zw, vcsStatus); err != nil {
		return errgo.Mask(err)
	}
	for _, c := range collections {
		w, err := zw.Create(historicalDBName + "/" + c.name + ".bson")
		if err != nil {
			return errgo.Mask(err)
		}
		if _, err := w.Write(c.data); err != nil {
			return errgo.Mask(err)
		}
	}
	return nil
}

// restoreDBFromFile reads the database dump from the given file
// and restores it into db.
func restoreDBFromFile(db *mgo.Database, filename string) (vcsStatus string, _ error) {
	f, err := os.Open(filename)
	if err != nil {
		return "", errgo.Mask(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", errgo.Mask(err)
	}
	zr, err := zip.NewReader(f, info.Size())
	if err != nil {
		return "", errgo.Mask(err)
	}
	var colls []collectionData
	for _, f := range zr.File {
		name := path.Clean(f.Name)
		if name == vcsStatusFile {
			data, err := readZipFile(f)
			if err != nil {
				return "", errgo.Mask(err)
			}
			vcsStatus = string(data)
			continue
		}
		if !strings.HasSuffix(name, ".bson") {
			logger.Infof("ignoring %v", name)
			continue
		}
		if !strings.HasPrefix(name, historicalDBName+"/") {
			return "", errgo.Newf("file %s from unknown database found in dump file", name)
		}
		name = strings.TrimPrefix(name, historicalDBName+"/")
		name = strings.TrimSuffix(name, ".bson")
		data, err := readZipFile(f)
		if err != nil {
			return "", errgo.Mask(err)
		}
		colls = append(colls, collectionData{
			name: name,
			data: data,
		})
	}
	if err := restoreDB(db, colls); err != nil {
		return "", errgo.Mask(err)
	}
	return vcsStatus, nil
}

// readZipFile reads the entire contents of f.
func readZipFile(f *zip.File) ([]byte, error) {
	r, err := f.Open()
	if err != nil {
		return nil, errgo.Mask(err)
	}
	defer r.Close()
	data, err := ioutil.ReadAll(r)
	if err != nil {
		return nil, errgo.Mask(err)
	}
	return data, nil
}

const vcsStatusFile = "vcs-status"

// writeVCSStatus writes the given VCS status into the
// given zip file.
func writeVCSStatus(zw *zip.Writer, vcsStatus string) error {
	w, err := zw.Create(vcsStatusFile)
	if err != nil {
		return errgo.Mask(err)
	}
	if _, err := w.Write([]byte(vcsStatus)); err != nil {
		return errgo.Mask(err)
	}
	return nil
}

const defaultCharmStoreRepo = "gopkg.in/juju/charmstore.v5-unstable"

// versionSpec specifies a version of the charm store to run
// and a function that will apply some updates to that
// version.
type versionSpec struct {
	version string
	// package holds the Go package containing the
	// charmd command. If empty, this defaults to
	//
	pkg string
	// update is called to apply updates after running charmd.
	update func(db *mgo.Database, csv *charmStoreVersion) error
}

var bogusPublicKey bakery.PublicKey

// runVersion runs the charm store at the given version
// and applies the associated updates.
func runMigrationVersion(db *mgo.Database, vc versionSpec) error {
	if vc.pkg == "" {
		vc.pkg = defaultCharmStoreRepo
	}
	csv, err := runCharmStoreVersion(vc.pkg, vc.version, &config.Config{
		MongoURL:          jujutesting.MgoServer.Addr(),
		AuthUsername:      "admin",
		AuthPassword:      "password",
		APIAddr:           fmt.Sprintf("localhost:%d", jujutesting.FindTCPPort()),
		MaxMgoSessions:    10,
		IdentityLocation:  "https://api.jujucharms.com/identity",
		IdentityPublicKey: &bogusPublicKey,
	})
	if err != nil {
		return errgo.Mask(err)
	}
	defer csv.Close()
	if vc.update == nil {
		return nil
	}
	if err := vc.update(db, csv); err != nil {
		return errgo.Notef(err, "cannot run update")
	}
	return nil
}

// collectionData holds all the dumped data from a collection.
type collectionData struct {
	// name holds the name of the collection.
	name string
	// data holds all the records from the collection as
	// a sequence of raw BSON records.
	data []byte
}

// dumpDB returns dumped data for all the non-system
// collections in the database.
func dumpDB(db *mgo.Database) ([]collectionData, error) {
	collections, err := db.CollectionNames()
	if err != nil {
		return nil, errgo.Mask(err)
	}
	sort.Strings(collections)
	var dumped []collectionData
	for _, c := range collections {
		if strings.HasPrefix(c, "system.") {
			continue
		}
		data, err := dumpCollection(db.C(c))
		if err != nil {
			return nil, errgo.Notef(err, "cannot dump %q: %v", c)
		}
		dumped = append(dumped, collectionData{
			name: c,
			data: data,
		})
	}
	return dumped, nil
}

// dumpCollection returns dumped data from a collection.
func dumpCollection(c *mgo.Collection) ([]byte, error) {
	var buf bytes.Buffer
	iter := c.Find(nil).Iter()
	var item bson.Raw
	for iter.Next(&item) {
		if item.Kind != 3 {
			return nil, errgo.Newf("unexpected item kind in collection %v", item.Kind)
		}
		buf.Write(item.Data)
	}
	if err := iter.Err(); err != nil {
		return nil, errgo.Mask(err)
	}
	return buf.Bytes(), nil
}

// restoreDB restores all the given collections into the database.
func restoreDB(db *mgo.Database, dump []collectionData) error {
	if err := db.DropDatabase(); err != nil {
		return errgo.Notef(err, "cannot drop database %v", db.Name)
	}
	for _, cd := range dump {
		if err := restoreCollection(db.C(cd.name), cd.data); err != nil {
			return errgo.Mask(err)
		}
	}
	return nil
}

// restoreCollection restores all the given data (in raw BSON format)
// into the given collection, dropping it first.
func restoreCollection(c *mgo.Collection, data []byte) error {
	if len(data) == 0 {
		return c.Create(&mgo.CollectionInfo{})
	}
	for len(data) > 0 {
		doc, rest := nextBSONDoc(data)
		data = rest
		if err := c.Insert(doc); err != nil {
			return errgo.Mask(err)
		}
	}
	return nil
}

// nextBSONDoc returns the next BSON document from
// the given data, and the data following it.
func nextBSONDoc(data []byte) (bson.Raw, []byte) {
	if len(data) < 4 {
		panic("truncated record")
	}
	n := binary.LittleEndian.Uint32(data)
	return bson.Raw{
		Kind: 3,
		Data: data[0:n],
	}, data[n:]
}

// charmStoreVersion represents a specific checked-out
// version of the charm store code and a running version
// of its associated charmd command.
type charmStoreVersion struct {
	tomb tomb.Tomb

	// rootDir holds the root of the GOPATH directory
	// holding all the charmstore source.
	// This is copied from the GOPATH directory
	// that the charmstore tests are being run in.
	rootDir string

	// csAddr holds the address that can be used to
	// dial the running charmd.
	csAddr string

	// runningCmd refers to the running charmd, so that
	// it can be killed.
	runningCmd *exec.Cmd
}

// runCharmStoreVersion runs the given charm store version
// from the given repository Go path and using the
// given configuration to start it with.
func runCharmStoreVersion(csRepo, version string, cfg *config.Config) (_ *charmStoreVersion, err error) {
	dir, err := ioutil.TempDir("", "charmstore-test")
	if err != nil {
		return nil, errgo.Mask(err)
	}
	defer func() {
		if err != nil {
			os.RemoveAll(dir)
		}
	}()
	csv := &charmStoreVersion{
		rootDir: dir,
		csAddr:  cfg.APIAddr,
	}
	if err := csv.copyRepo(csRepo); err != nil {
		return nil, errgo.Mask(err)
	}
	destPkgDir := filepath.Join(csv.srcDir(), filepath.FromSlash(csRepo))

	// Discard any changes made in the local repo.
	if err := csv.runCmd(destPkgDir, "git", "reset", "--hard", "HEAD"); err != nil {
		return nil, errgo.Mask(err)
	}

	if err := csv.runCmd(destPkgDir, "git", "checkout", version); err != nil {
		return nil, errgo.Mask(err)
	}
	depFile := filepath.Join(destPkgDir, "dependencies.tsv")
	if err := csv.copyDeps(depFile); err != nil {
		return nil, errgo.Mask(err)
	}
	if err := csv.runCmd(destPkgDir, "godeps", "-force-clean", "-u", depFile); err != nil {
		return nil, errgo.Mask(err)
	}
	if err := csv.runCmd(destPkgDir, "go", "install", path.Join(csRepo, "/cmd/charmd")); err != nil {
		return nil, errgo.Mask(err)
	}
	if err := csv.startCS(cfg); err != nil {
		return nil, errgo.Mask(err)
	}
	return csv, nil
}

// srvDir returns the package root of the charm store source.
func (csv *charmStoreVersion) srcDir() string {
	return filepath.Join(csv.rootDir, "src")
}

// Close kills the charmd and removes all its associated files.
func (csv *charmStoreVersion) Close() error {
	csv.Kill()
	if err := csv.Wait(); err != nil {
		logger.Infof("warning: error closing down server: %#v", err)
	}
	return csv.remove()
}

// remove removes all the files associated with csv.
func (csv *charmStoreVersion) remove() error {
	return os.RemoveAll(csv.rootDir)
}

// uploadSpec specifies a entity to be uploaded through
// the API.
type uploadSpec struct {
	// usePost specifies that POST should be used rather than PUT.
	usePost bool
	// entity holds the entity to be uploaded.
	entity ArchiverTo
	// id holds the id to be uploaded to. If PUT is used,
	// this must be parsable using MustParseResolvedURL;
	// otherwise it must be a valid charm id with no revision.
	id string
}

// Upload uploads all the given entities to the charm store,
// using the given API version.
func (csv *charmStoreVersion) Upload(apiVersion string, specs []uploadSpec) error {
	for _, spec := range specs {
		if spec.usePost {
			if err := csv.uploadWithPost(apiVersion, spec.entity, charm.MustParseURL(spec.id)); err != nil {
				return errgo.Mask(err)
			}
		} else {
			if err := csv.uploadWithPut(apiVersion, spec.entity, MustParseResolvedURL(spec.id)); err != nil {
				return errgo.Mask(err)
			}
		}
	}
	return nil
}

func (csv *charmStoreVersion) uploadWithPost(apiVersion string, entity ArchiverTo, url *charm.URL) error {
	var buf bytes.Buffer
	if err := entity.ArchiveTo(&buf); err != nil {
		return errgo.Mask(err)
	}
	hash := blobstore.NewHash()
	hash.Write(buf.Bytes())
	logger.Infof("archive %d bytes", len(buf.Bytes()))
	req, err := http.NewRequest("POST", fmt.Sprintf("/%s/%s/archive?hash=%x", apiVersion, url.Path(), hash.Sum(nil)), &buf)
	if err != nil {
		return errgo.Mask(err)
	}
	req.Header.Set("Content-Type", "application/zip")
	resp, err := csv.DoRequest(req)
	if err != nil {
		return errgo.Mask(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := ioutil.ReadAll(resp.Body)
		return errgo.Newf("unexpected response to POST %q: %v (body %q)", req.URL, resp.Status, body)
	}
	return nil
}

func (csv *charmStoreVersion) uploadWithPut(apiVersion string, entity ArchiverTo, id *router.ResolvedURL) error {
	var buf bytes.Buffer
	if err := entity.ArchiveTo(&buf); err != nil {
		return errgo.Mask(err)
	}
	promulgatedParam := ""
	if pid := id.PromulgatedURL(); pid != nil {
		promulgatedParam = fmt.Sprintf("&promulgated=%s", pid)
	}
	hash := blobstore.NewHash()
	hash.Write(buf.Bytes())
	logger.Infof("archive %d bytes", len(buf.Bytes()))
	req, err := http.NewRequest("PUT", fmt.Sprintf("/%s/%s/archive?hash=%x%s", apiVersion, id.URL.Path(), hash.Sum(nil), promulgatedParam), &buf)
	if err != nil {
		return errgo.Mask(err)
	}
	req.Header.Set("Content-Type", "application/zip")
	resp, err := csv.DoRequest(req)
	if err != nil {
		return errgo.Mask(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := ioutil.ReadAll(resp.Body)
		return errgo.Newf("unexpected response to PUT %q: %v (body %q)", req.URL, resp.Status, body)
	}
	return nil
}

// Put makes a PUT request containing the given body, JSON encoded, to the API.
// The urlPath parameter should contain only the URL path, not the host or scheme.
func (csv *charmStoreVersion) Put(urlPath string, body interface{}) error {
	data, err := json.Marshal(body)
	if err != nil {
		return errgo.Mask(err)
	}
	req, err := http.NewRequest("PUT", urlPath, bytes.NewReader(data))
	if err != nil {
		return errgo.Mask(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := csv.DoRequest(req)
	if err != nil {
		return errgo.Mask(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := ioutil.ReadAll(resp.Body)
		return errgo.Newf("unexpected response to PUT %q: %v (body %q)", req.URL, resp.Status, body)
	}
	return nil
}

// DoRequest sends the given HTTP request to the charm store server.
func (csv *charmStoreVersion) DoRequest(req *http.Request) (*http.Response, error) {
	req.SetBasicAuth("admin", "password")
	req.URL.Host = csv.csAddr
	req.URL.Scheme = "http"
	return http.DefaultClient.Do(req)
}

// waitUntilServerIsUp waits until the charmstore server is up.
// It returns an error if it has to wait longer than the given timeout.
func (csv *charmStoreVersion) waitUntilServerIsUp(timeout time.Duration) error {
	endt := time.Now().Add(timeout)
	for {
		req, err := http.NewRequest("GET", "/", nil)
		if err != nil {
			return errgo.Mask(err)
		}
		resp, err := csv.DoRequest(req)
		if err == nil {
			resp.Body.Close()
			return nil
		}
		if time.Now().After(endt) {
			return errgo.Notef(err, "timed out waiting for server to come up")
		}
		time.Sleep(100 * time.Millisecond)
	}

}

// startCS starts the charmd process running.
func (csv *charmStoreVersion) startCS(cfg *config.Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return errgo.Mask(err)
	}
	cfgPath := filepath.Join(csv.rootDir, "csconfig.yaml")
	if err := ioutil.WriteFile(cfgPath, data, 0666); err != nil {
		return errgo.Mask(err)
	}
	cmd := exec.Command(filepath.Join(csv.rootDir, "bin", "charmd"), "--logging-config=INFO", cfgPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir = csv.rootDir
	if err := cmd.Start(); err != nil {
		return errgo.Mask(err)
	}
	csv.runningCmd = cmd
	csv.tomb.Go(func() error {
		return errgo.Mask(cmd.Wait())
	})
	if err := csv.waitUntilServerIsUp(10 * time.Second); err != nil {
		return errgo.Mask(err)
	}
	return nil
}

// Kill kills the charmstore server.
func (csv *charmStoreVersion) Kill() {
	csv.runningCmd.Process.Kill()
}

// Wait waits for the charmstore server to exit.
func (csv *charmStoreVersion) Wait() error {
	return csv.tomb.Wait()
}

// runCmd runs the given command in the given current
// working directory.
func (csv *charmStoreVersion) runCmd(cwd string, c string, arg ...string) error {
	logger.Infof("cd %v; %v %v", cwd, c, strings.Join(arg, " "))
	cmd := exec.Command(c, arg...)
	cmd.Env = envWithVars(map[string]string{
		"GOPATH": csv.rootDir,
	})
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir = cwd
	if err := cmd.Run(); err != nil {
		return errgo.Notef(err, "failed to run %v %v", c, arg)
	}
	return nil
}

// envWithVars returns the OS environment variables
// with the specified variables changed to their associated
// values.
func envWithVars(vars map[string]string) []string {
	env := os.Environ()
	for i, v := range env {
		j := strings.Index(v, "=")
		if j == -1 {
			continue
		}
		name := v[0:j]
		if val, ok := vars[name]; ok {
			env[i] = name + "=" + val
			delete(vars, name)
		}
	}
	for name, val := range vars {
		env = append(env, name+"="+val)
	}
	return env
}

// copyDeps copies all the dependencies found in the godeps
// file depFile from the local version into csv.rootDir.
func (csv *charmStoreVersion) copyDeps(depFile string) error {
	f, err := os.Open(depFile)
	if err != nil {
		return errgo.Mask(err)
	}
	defer f.Close()
	for scan := bufio.NewScanner(f); scan.Scan(); {
		line := scan.Text()
		tabIndex := strings.Index(line, "\t")
		if tabIndex == -1 {
			return errgo.Newf("no tab found in dependencies line %q", line)
		}
		pkgPath := line[0:tabIndex]
		if err := csv.copyRepo(pkgPath); err != nil {
			return errgo.Mask(err)
		}
	}
	return nil
}

// copyRepo copies all the files inside the given importPath
// from their local version into csv.rootDir.
func (csv *charmStoreVersion) copyRepo(importPath string) error {
	pkg, err := build.Import(importPath, ".", build.FindOnly)
	if pkg.Dir == "" {
		return errgo.Mask(err)
	}
	destDir := filepath.Join(csv.srcDir(), filepath.FromSlash(pkg.ImportPath))
	if err := os.MkdirAll(filepath.Dir(destDir), 0777); err != nil {
		return errgo.Mask(err)
	}
	if err := fs.Copy(pkg.Dir, destDir); err != nil {
		return errgo.Mask(err)
	}
	return nil
}
