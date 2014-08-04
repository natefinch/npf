package storetesting

import (
	"flag"
	"os"

	jujutesting "github.com/juju/testing"
)

var noTestMongoJs *bool = flag.Bool("notest-mongojs", false, "Disable MongoDB tests that require javascript")

func init() {
	if os.Getenv("JUJU_NOTEST_MONGOJS") == "1" || jujutesting.MgoServer.WithoutV8 {
		*noTestMongoJs = true
	}
}

// MongoJSEnabled reports whether testing code should run tests
// that rely on Javascript inside MongoDB.
func MongoJSEnabled() bool {
	return !*noTestMongoJs
}
