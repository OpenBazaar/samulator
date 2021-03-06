package builder

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/OpenBazaar/mason/builder/blueprints"
	"github.com/OpenBazaar/mason/builder/cacher"
	"github.com/OpenBazaar/mason/builder/runner"
	"github.com/OpenBazaar/mason/util"
	"github.com/op/go-logging"
	shell "github.com/placer14/go-shell"
)

const GO_BUILD_VERSION = "1.11"

var log = logging.MustGetLogger("builder")

type openBazaarBuilder struct {
	sync.Mutex

	cachePath        string
	friendlyLabel    string
	versionReference string
	workDir          string
	targetOS         string
	targetArch       string
}

func NewOpenBazaarDaemon(label, version string) *openBazaarBuilder {
	var homeDir = os.Getenv("HOME")
	if homeDir == "" {
		log.Warningf("HOME is unset, using current path")
		homeDir = "."
	}
	return &openBazaarBuilder{
		friendlyLabel:    label,
		versionReference: version,
		cachePath:        filepath.Join(homeDir, ".mason", "cache"),
	}
}

func (b *openBazaarBuilder) Build() (*runner.OpenBazaarRunner, error) {
	c, err := cacher.OpenOrCreate(b.cachePath)
	if err != nil {
		log.Warningf("failed opening cache (%s): %s", b.cachePath, err.Error())
	}
	if runnerPath, err := c.Get("openbazaard", b.versionReference); err == nil {
		return runner.FromBinaryPath(runnerPath)
	}

	b.Lock()
	defer b.Unlock()

	b.workDir = util.GenerateTempBuildPath(b.friendlyLabel)
	log.Infof("building at %s", b.workDir)

	src, err := blueprints.InflateOpenBazaarDaemon(b.workDir)
	if err != nil {
		return nil, fmt.Errorf("inflating source: %s", err.Error())
	}

	if err := src.CheckoutVersion(b.versionReference); err != nil {
		return nil, fmt.Errorf("checkout version: %s", err.Error())
	}

	buildPath, err := generateOSSpecificBuild(src)
	if err != nil {
		return nil, fmt.Errorf("building for %s: %s", runtime.GOOS, err.Error())
	}

	if err := c.Cache("openbazaard", b.versionReference, buildPath); err != nil {
		log.Warningf("failed caching build for %s (%s): %s", "openbazaard", b.versionReference, err.Error())
		return nil, fmt.Errorf("caching build: %s", err.Error())
	}

	runnerPath, err := c.Get("openbazaard", b.versionReference)
	if err != nil {
		return nil, fmt.Errorf("retrieving cached build: %s", err.Error())
	}
	return runner.FromBinaryPath(runnerPath)
}

func generateOSSpecificBuild(src *blueprints.OpenBazaarSource) (string, error) {
	var (
		getXGo      = shell.Cmd("go", "get", "github.com/karalabe/xgo")
		buildBinary = shell.Cmd(
			fmt.Sprintf("GOPATH=%s", src.WorkDir()),
			"xgo", "-v", "-targets", util.GetXGoBuildTarget(), // build arch/OS targets
			"-dest=./dest",             // build destination path
			"-out", src.BinaryPrefix(), // binary name prefix
			"-go", GO_BUILD_VERSION, // specific go build version
			filepath.Join(src.WorkDir(), "src", "github.com", "OpenBazaar", "openbazaar-go"),
		)
		buildCommands = []*shell.Command{getXGo, buildBinary}
	)
	for _, cmd := range buildCommands {
		var proc = cmd.SetWorkDir(src.WorkDir()).Start()
		if err := proc.Wait(); err != nil {
			return "", fmt.Errorf("(%v) waiting: %s", proc, err.Error())
		}
		if proc.ExitStatus != 0 {
			return "", fmt.Errorf("non-zero build exit: %s", proc.Error())
		}
	}
	return binaryPath(src), nil
}

func binaryPath(src *blueprints.OpenBazaarSource) string {
	var (
		targets        = strings.Split(util.GetXGoBuildTarget(), "/")
		os, arch       = targets[0], targets[1]
		binaryFilename = fmt.Sprintf("%s-%s-10.6-%s", src.BinaryPrefix(), os, arch)
	)
	return filepath.Join(src.WorkDir(), "dest", binaryFilename)
}

func (b *openBazaarBuilder) MustClean() {
	if b.workDir == "" {
		return
	}
	if err := os.RemoveAll(b.workDir); err != nil {
		log.Errorf("cleaning (%s): %s", b.workDir, err.Error())
		panic(err.Error())
	}
}
