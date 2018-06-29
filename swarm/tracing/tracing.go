package tracing

import (
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/log"
	cli "gopkg.in/urfave/cli.v1"
)

var Enabled bool = false

// TracingEnabledFlag is the CLI flag name to use to enable trace collections.
const TracingEnabledFlag = "tracing"

var (
	TracingFlag = cli.BoolFlag{
		Name:  TracingEnabledFlag,
		Usage: "Enable tracing",
	}
)

// Flags holds all command-line flags required for tracing collection.
var Flags = []cli.Flag{
	TracingFlag,
}

// Init enables or disables the metrics system. Since we need this to run before
// any other code gets to create meters and timers, we'll actually do an ugly hack
// and peek into the command line args for the metrics flag.
func init() {
	for _, arg := range os.Args {
		if flag := strings.TrimLeft(arg, "-"); flag == TracingEnabledFlag {
			log.Info("Enabling opentracing")
			Enabled = true
		}
	}
}
