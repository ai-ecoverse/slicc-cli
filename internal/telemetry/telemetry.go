package telemetry

import (
	"os"
	"strings"
	"sync/atomic"
	"time"

	optel "github.com/ai-ecoverse/go-optel"
)

const (
	AppID     = "slicc-cli"
	OptOutEnv = "SLICC_NO_TELEMETRY"
	URLEnv    = "SLICC_TELEMETRY_URL"
)

type Source struct{ name string }

func (s Source) String() string { return s.name }

var (
	Update  = Source{"update"}
	Signal  = Source{"signal"}
	Link    = Source{"link"}
	Browser = Source{"browser"}
	SSH     = Source{"ssh"}
	CDP     = Source{"cdp"}
	Install = Source{"install"}
)

var commands = map[string]bool{
	"abort":    true,
	"attach":   true,
	"browsers": true,
	"follow":   true,
	"install":  true,
	"launch":   true,
	"open":     true,
	"pair":     true,
	"prompt":   true,
	"unpair":   true,
	"update":   true,
	"watch":    true,
}

var client atomic.Pointer[optel.Client]

func Enabled(release bool) bool {
	return release && os.Getenv(OptOutEnv) == ""
}

func Command(args []string) string {
	if len(args) == 0 {
		return "default"
	}
	sub := args[0]
	if strings.Contains(sub, "://") {
		if len(args) < 2 {
			return "join"
		}
		sub = args[1]
	}
	if commands[sub] {
		return sub
	}
	return "unknown"
}

func Start(release bool, args []string) func() {
	if !Enabled(release) {
		return func() {}
	}
	c := optel.Configure(AppID, optel.Options{CollectBaseURL: os.Getenv(URLEnv)})
	client.Store(c)
	c.Sample(optel.Enter, Command(args), "")
	return func() { c.Flush(2 * time.Second) }
}

func ReportError(source Source, err error) {
	if source.name == "" {
		return
	}
	client.Load().ReportError(source.name, err)
}
