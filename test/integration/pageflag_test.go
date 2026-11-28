package integration

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

var pageFlagTargets = []protectOverlayRun{
	{
		pkg:     "./internal/flow",
		file:    "internal/flow/pageflag_test.go",
		regress: "test/integration/testdata/pageflag/flow.go",
		run:     "^Test(Page|Origin)Flag",
		timeout: 6 * time.Minute,
		race:    true,
	},
	{
		pkg:     "./cmd/slicc",
		file:    "cmd/slicc/pageflag_test.go",
		regress: "test/integration/testdata/pageflag/open.go",
		run:     "^Test(Page|Origin)Flag",
		timeout: 15 * time.Minute,
		race:    true,
	},
}

const (
	pageFlagSource        = "internal/flow/run.go"
	pageFlagCheck         = "\tif !browser.IsSevenOrigin(o, nil) && protect.MustNew(nil).Loaded(o) {\n"
	pageFlagDefault       = "cmd/slicc/default.go"
	pageFlagParse         = "\tif _, err := flow.PageOrigins(f.page, f.origins); err != nil {\n\t\tfmt.Fprintf(stderr, \"slicc: %v\\n\", err)\n\t\treturn f, 2\n\t}\n"
	pageFlagRefuse        = "TestPageFlagRefusesProtected/"
	pageFlagAccept        = "TestPageFlagStillAccepts/"
	pageFlagListen        = "TestPageFlagRefusalHappensBeforeListen/"
	pageFlagFlow          = "TestPageFlagFlowRefusesBeforeLaunch/"
	originFlagSource      = "internal/signal/origin.go"
	originFlagCheck       = "\t\tif protectedExtra(n, p) {\n\t\t\treturn nil, fmt.Errorf(\"--origin %s is on a protected host and is not a seven page\", n)\n\t\t}\n"
	originFlagPred        = "func protectedExtra(n string, p *protect.Origins) bool {\n\tu, err := url.Parse(n)\n\tif err != nil || u.Scheme == \"\" {\n\t\treturn true\n\t}\n\tswitch strings.ToLower(u.Scheme) {\n\tcase \"http\", \"https\":\n\t\treturn !browser.IsSevenOrigin(n, nil) && p.Loaded(n)\n\tdefault:\n\t\th := protect.Host(n)\n\t\tif h == \"\" {\n\t\t\th = authorityHost(u)\n\t\t}\n\t\tif h == \"\" {\n\t\t\treturn true\n\t\t}\n\t\tproj := \"https://\" + h\n\t\treturn p.URL(proj) || !protect.Parses(proj) && protect.ZoneHost(h)\n\t}\n}\n"
	originFlagAppend      = "\t\to.extra = append(o.extra, n)\n"
	originFlagCDP         = "cmd/slicc/cdp.go"
	originFlagCDPCheck    = "\tif _, err := signal.NewOrigins(f.origins); err != nil {\n\t\tfmt.Fprintf(aio.stderr, \"slicc cdp: %v\\n\", err)\n\t\treturn f, 2\n\t}\n"
	originFlagAttach      = "cmd/slicc/attach.go"
	originFlagAttachCheck = "\torigins, err := signal.NewOrigins(f.origins)\n\tif err != nil {\n\t\treturn fail(err, 2)\n\t}\n\tif !f.noKernel {\n\t\tf.kernelPort = kernelin.Pick(f.kernelPort, status)\n\t}\n"
	originFlagRefuse      = "TestOriginFlagRefusesProtected/"
	originFlagAccept      = "TestOriginFlagStillAccepts/"
	originFlagListen      = "TestOriginFlagRefusalHappensBeforeListen/"
	originFlagCmdOK       = "TestOriginFlagStillAcceptsCommands/"
	zoneHostSource        = "internal/protect/protect.go"
	zoneHostFold          = "\th = strings.Map(func(r rune) rune {\n\t\tswitch r {\n\t\tcase '\\u3002', '\\uff0e', '\\uff61':\n\t\t\treturn '.'\n\t\tdefault:\n\t\t\treturn r\n\t\t}\n\t}, h)\n"
)

var pageFlagManifest = []string{
	pageFlagRefuse + "Vault",
	pageFlagRefuse + "VaultPreview",
	pageFlagRefuse + "VaultMain",
	pageFlagRefuse + "PilotExact",
	pageFlagRefuse + "PilotUpper",
	pageFlagRefuse + "PilotTrailingDot",
	pageFlagRefuse + "Pilot443",
	pageFlagRefuse + "PilotDeeperName",
	pageFlagRefuse + "PilotHTTP",
	pageFlagRefuse + "PilotFullwidth",
	pageFlagRefuse + "UserinfoPilot",
	pageFlagRefuse + "OtherProtectedZoneHost",
	pageFlagRefuse + "Apex",
	pageFlagRefuse + "SevenHTTP",
	pageFlagRefuse + "SevenOtherPort",
	pageFlagRefuse + "SevenTrailingDot",
	pageFlagRefuse + "UnparseableZoneHost",
	pageFlagRefuse + "VaultAlsoOrigin",
	pageFlagRefuse + "VaultPort99999",
	pageFlagAccept + "SevenPage",
	pageFlagAccept + "DefaultPage",
	pageFlagAccept + "SixPage",
	pageFlagAccept + "SevenUpper",
	pageFlagAccept + "Seven443",
	pageFlagAccept + "SevenWithOrigin",
	pageFlagAccept + "LocalhostOrdinary",
	pageFlagAccept + "LocalhostWithOrigin",
	pageFlagAccept + "NonSliccyHTTPS",
	pageFlagAccept + "SliccyLookalikeOtherZone",
	pageFlagFlow + "Refused",
	pageFlagFlow + "SevenReachesAttach",
	pageFlagListen + "Open",
	pageFlagListen + "Pair",
	pageFlagListen + "DefaultBrowser",
	pageFlagListen + "DefaultMode",
	pageFlagListen + "SevenListens",
	"TestPageFlagRefusalNamesPageNotOrigin",
	originFlagRefuse + "Pilot",
	originFlagRefuse + "Vault",
	originFlagRefuse + "Deeper",
	originFlagRefuse + "Apex",
	originFlagRefuse + "PilotHTTP",
	originFlagRefuse + "SevenHTTP",
	originFlagRefuse + "PilotWS",
	originFlagRefuse + "VaultWSS",
	originFlagRefuse + "DeeperFTP",
	originFlagRefuse + "ApexFTP",
	originFlagRefuse + "PilotUpper",
	originFlagRefuse + "PilotTrailingDot",
	originFlagRefuse + "Pilot443",
	originFlagRefuse + "PilotOtherPort",
	originFlagRefuse + "PilotFullwidth",
	originFlagRefuse + "SevenOtherPort",
	originFlagRefuse + "SevenTrailingDot",
	originFlagRefuse + "ExtZoneHost",
	originFlagRefuse + "UnparseableZone",
	originFlagRefuse + "VaultPort99999",
	originFlagRefuse + "FileZonePort",
	originFlagRefuse + "WSPort99999",
	originFlagRefuse + "WSSUnparseableZone",
	originFlagRefuse + "WSSUnparseableZoneSep",
	originFlagRefuse + "WSSApexIdeographicDot",
	originFlagRefuse + "FileUnparseableZone",
	originFlagRefuse + "EmptyHostWS",
	originFlagRefuse + "SecondExtra",
	originFlagRefuse + "Userinfo",
	originFlagAccept + "Seven",
	originFlagAccept + "WWW",
	originFlagAccept + "Localhost",
	originFlagAccept + "NonSliccy",
	originFlagAccept + "ChromeExtension",
	originFlagAccept + "FileLocalhost",
	originFlagAccept + "Devtools",
	originFlagAccept + "DevtoolsOpaqueHost",
	originFlagAccept + "WSNumericLabel",
	originFlagAccept + "WSNonZone",
	originFlagAccept + "Seven443",
	originFlagAccept + "SevenUpper",
	originFlagAccept + "PlacementMarker",
	originFlagAccept + "OwnerAdmission",
	originFlagAccept + "BiosLiveCheck",
	originFlagAccept + "EvaluateResultUnmarked",
	"TestOriginFlagAdmitsAccepted",
	originFlagListen + "AttachPilot",
	originFlagListen + "AttachVault",
	originFlagListen + "AttachDeeper",
	originFlagListen + "AttachApex",
	originFlagListen + "AttachPilotWS",
	originFlagListen + "AttachBeforePick",
	originFlagListen + "CdpPilot",
	originFlagListen + "CdpVault",
	originFlagListen + "CdpDeeper",
	originFlagListen + "CdpApex",
	originFlagListen + "CdpVaultWSS",
	originFlagListen + "AgentPilot",
	originFlagListen + "AgentVault",
	originFlagListen + "AgentDeeper",
	originFlagListen + "AgentApex",
	originFlagListen + "AgentDeeperFTP",
	originFlagListen + "OpenPilot",
	originFlagListen + "OpenVault",
	originFlagListen + "OpenDeeper",
	originFlagListen + "OpenApex",
	originFlagListen + "OpenApexFTP",
	originFlagListen + "DefaultPilot",
	originFlagListen + "DefaultVault",
	originFlagListen + "DefaultDeeper",
	originFlagListen + "DefaultApex",
	originFlagListen + "DefaultPilotWS",
	originFlagCmdOK + "AttachSeven",
	originFlagCmdOK + "AttachNonSliccy",
	originFlagCmdOK + "AttachChromeExtension",
	originFlagCmdOK + "AttachWSNonZone",
	originFlagCmdOK + "CdpSeven",
	originFlagCmdOK + "CdpLocalhost",
	originFlagCmdOK + "AgentWWW",
	originFlagCmdOK + "AgentFile",
	originFlagCmdOK + "AgentDevtools",
	originFlagCmdOK + "OpenSevenOrigin",
	originFlagCmdOK + "OpenNonSliccy",
	originFlagCmdOK + "OpenChromeExtension",
	originFlagCmdOK + "DefaultWSNonZone",
	originFlagCmdOK + "DefaultLocalhost",
}

var pageFlagRow = regexp.MustCompile(`(?m)^\s*--- (PASS|FAIL|SKIP): (Test(?:Page|Origin)Flag\S+) \(`)

func pageFlagResults(t *testing.T, files map[string]string) (map[string]string, string) {
	t.Helper()
	all := map[string]string{}
	var outputs []string
	var exitFail bool
	for _, target := range pageFlagTargets {
		out, err := protectOverlay(t, target, nil, files)
		outputs = append(outputs, out)
		if err != nil {
			exitFail = true
		}
		if !strings.Contains(out, "=== RUN   Test") {
			t.Cleanup(func() {
				if t.Failed() {
					t.Log(strings.Join(outputs, "\n"))
				}
			})
			return nil, "build or run produced no Test output"
		}
		if strings.Contains(out, "\npanic:") || strings.Contains(out, "\n\tpanic:") {
			t.Cleanup(func() {
				if t.Failed() {
					t.Log(strings.Join(outputs, "\n"))
				}
			})
			return nil, "panic"
		}
		for _, m := range pageFlagRow.FindAllStringSubmatch(out, -1) {
			if _, dup := all[m[2]]; dup {
				t.Errorf("row %s reported twice", m[2])
			}
			all[m[2]] = m[1]
		}
	}
	leaves := map[string]string{}
	for name, result := range all {
		leaf := true
		for other := range all {
			leaf = leaf && !strings.HasPrefix(other, name+"/")
		}
		if leaf {
			leaves[name] = result
		}
	}
	fails := 0
	for _, result := range leaves {
		if result == "FAIL" {
			fails++
		}
		if result == "SKIP" {
			t.Cleanup(func() {
				if t.Failed() {
					t.Log(strings.Join(outputs, "\n"))
				}
			})
			return leaves, "SKIP leaf"
		}
	}
	unclean := ""
	switch {
	case exitFail && fails == 0:
		unclean = "exit 1 with no FAIL leaves"
	case !exitFail && fails > 0:
		unclean = "exit 0 with FAIL leaves"
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(strings.Join(outputs, "\n"))
		}
	})
	return leaves, unclean
}

func pageFlagBaseline(t *testing.T) map[string]string {
	t.Helper()
	got, unclean := pageFlagResults(t, nil)
	if unclean != "" {
		t.Errorf("the unmutated rows were unclean: %s", unclean)
	}
	for _, row := range pageFlagManifest {
		if got[row] != "PASS" {
			t.Errorf("row %s: %q, want PASS", row, got[row])
		}
	}
	for row, result := range got {
		if !slices.Contains(pageFlagManifest, row) {
			t.Errorf("row %s (%s) is not in the manifest", row, result)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
	return got
}

func TestPageFlagRows(t *testing.T) {
	pageFlagBaseline(t)
}

const pageFlagMutantCount = 19

type pageFlagMutant struct {
	group, name, file, from, to string
	named, incidental           []string
}

func pageFlagMutants() []pageFlagMutant {
	refusals := func(names ...string) []string {
		var rows []string
		for _, n := range names {
			rows = append(rows, pageFlagRefuse+n)
		}
		return rows
	}
	originRefusals := func(names ...string) []string {
		var rows []string
		for _, n := range names {
			rows = append(rows, originFlagRefuse+n)
		}
		return rows
	}
	originListen := func(names ...string) []string {
		var rows []string
		for _, n := range names {
			rows = append(rows, originFlagListen+n)
		}
		return rows
	}
	cmdOK := func(names ...string) []string {
		var rows []string
		for _, n := range names {
			rows = append(rows, originFlagCmdOK+n)
		}
		return rows
	}
	allOriginRefusals := slices.Concat(
		originRefusals("Pilot", "Vault", "Deeper", "Apex", "PilotHTTP", "SevenHTTP", "PilotWS", "VaultWSS", "DeeperFTP", "ApexFTP", "PilotUpper", "PilotTrailingDot", "Pilot443", "PilotOtherPort", "PilotFullwidth", "SevenOtherPort", "SevenTrailingDot", "ExtZoneHost", "UnparseableZone", "VaultPort99999", "FileZonePort", "WSPort99999", "WSSUnparseableZone", "WSSUnparseableZoneSep", "WSSApexIdeographicDot", "FileUnparseableZone", "EmptyHostWS", "SecondExtra"),
		originListen("AttachPilot", "AttachVault", "AttachDeeper", "AttachApex", "AttachPilotWS", "AttachBeforePick", "CdpPilot", "CdpVault", "CdpDeeper", "CdpApex", "CdpVaultWSS", "AgentPilot", "AgentVault", "AgentDeeper", "AgentApex", "AgentDeeperFTP", "OpenPilot", "OpenVault", "OpenDeeper", "OpenApex", "OpenApexFTP", "DefaultPilot", "DefaultVault", "DefaultDeeper", "DefaultApex", "DefaultPilotWS"),
	)
	pageNamesOrigin := []string{"TestPageFlagRefusalNamesPageNotOrigin"}
	sevenPageOpen := slices.Concat(
		originListen("OpenPilot", "OpenVault", "OpenDeeper", "OpenApex", "OpenApexFTP", "DefaultPilot", "DefaultVault", "DefaultDeeper", "DefaultApex", "DefaultPilotWS"),
		cmdOK("OpenSevenOrigin", "OpenNonSliccy", "OpenChromeExtension", "DefaultWSNonZone", "DefaultLocalhost"),
		pageNamesOrigin,
	)
	nonHTTPZone := originRefusals("PilotWS", "VaultWSS", "DeeperFTP", "ApexFTP", "ExtZoneHost", "FileZonePort", "WSPort99999", "WSSUnparseableZone", "WSSUnparseableZoneSep", "WSSApexIdeographicDot", "FileUnparseableZone", "EmptyHostWS")
	nonHTTPZoneListen := originListen("AttachPilotWS", "CdpVaultWSS", "AgentDeeperFTP", "OpenApexFTP", "DefaultPilotWS")
	return []pageFlagMutant{
		{"PageGuard", "ProtectedCheckRemoved", pageFlagSource, pageFlagCheck, "\tif false {\n", slices.Concat(
			refusals("Vault", "VaultPreview", "VaultMain", "PilotExact", "PilotUpper", "PilotTrailingDot", "Pilot443", "PilotDeeperName", "PilotHTTP", "PilotFullwidth", "UserinfoPilot", "OtherProtectedZoneHost", "Apex", "SevenHTTP", "SevenOtherPort", "SevenTrailingDot", "UnparseableZoneHost", "VaultAlsoOrigin", "VaultPort99999"),
			[]string{pageFlagFlow + "Refused", pageFlagListen + "Open", pageFlagListen + "Pair", pageFlagListen + "DefaultBrowser", pageFlagListen + "DefaultMode"},
			pageNamesOrigin,
		), nil},
		{"PageGuard", "CatalogExemptionRemoved", pageFlagSource, pageFlagCheck, "\tif protect.MustNew(nil).Loaded(o) {\n", slices.Concat(
			[]string{
				pageFlagAccept + "SevenPage", pageFlagAccept + "DefaultPage", pageFlagAccept + "SixPage", pageFlagAccept + "SevenUpper", pageFlagAccept + "Seven443", pageFlagAccept + "SevenWithOrigin",
				pageFlagFlow + "SevenReachesAttach", pageFlagListen + "SevenListens",
			},
			sevenPageOpen,
		), nil},
		{"PageGuard", "ExactHostMatchOnly", pageFlagSource, pageFlagCheck, "\tif u.Host == \"lease-pilot.sliccy.ai\" {\n", slices.Concat(
			refusals("Vault", "VaultPreview", "VaultMain", "PilotUpper", "PilotTrailingDot", "Pilot443", "PilotDeeperName", "PilotFullwidth", "OtherProtectedZoneHost", "Apex", "SevenHTTP", "SevenOtherPort", "SevenTrailingDot", "UnparseableZoneHost", "VaultAlsoOrigin", "VaultPort99999"),
			[]string{pageFlagFlow + "Refused", pageFlagListen + "Pair", pageFlagListen + "DefaultBrowser", pageFlagListen + "DefaultMode"},
			pageNamesOrigin,
		), nil},
		{"PageGuard", "ExemptionCountsOrigins", pageFlagSource, pageFlagCheck, "\tif !browser.IsSevenOrigin(o, extra) && protect.MustNew(nil).Loaded(o) {\n", refusals("VaultAlsoOrigin"), nil},
		{"PageGuard", "LoadedNarrowedToURL", pageFlagSource, pageFlagCheck, "\tif !browser.IsSevenOrigin(o, nil) && protect.MustNew(nil).URL(o) {\n", refusals("UnparseableZoneHost", "VaultPort99999"), nil},
		{"CommandOrder", "ParseDefaultCheckRemoved", pageFlagDefault, pageFlagParse, "", slices.Concat(
			[]string{pageFlagListen + "DefaultMode"},
			originListen("DefaultPilot", "DefaultVault", "DefaultDeeper", "DefaultApex", "DefaultPilotWS"),
		), nil},
		{"Extras", "NewOriginsCheckRemoved", originFlagSource, originFlagCheck, "\t\t_ = p\n", slices.Concat(allOriginRefusals, pageNamesOrigin), nil},
		{"Extras", "OnlyPageChecked", originFlagSource, originFlagPred, "func protectedExtra(n string, p *protect.Origins) bool {\n\treturn false\n}\n", slices.Concat(allOriginRefusals, pageNamesOrigin), nil},
		{"Extras", "ExtraEntriesSkipped", originFlagSource, originFlagCheck, "\t\tif len(o.extra) == 0 && protectedExtra(n, p) {\n\t\t\treturn nil, fmt.Errorf(\"--origin %s is on a protected host and is not a seven page\", n)\n\t\t}\n", originRefusals("SecondExtra"), nil},
		{"Schemes", "SchemeLimitRemoved", originFlagSource, originFlagPred, "func protectedExtra(n string, p *protect.Origins) bool {\n\treturn !browser.IsSevenOrigin(n, nil) && p.Loaded(n)\n}\n", slices.Concat(
			[]string{originFlagAccept + "ChromeExtension", originFlagAccept + "PlacementMarker", originFlagAccept + "OwnerAdmission", originFlagAccept + "BiosLiveCheck", originFlagAccept + "EvaluateResultUnmarked", originFlagAccept + "FileLocalhost", originFlagAccept + "Devtools", originFlagAccept + "DevtoolsOpaqueHost", "TestOriginFlagAdmitsAccepted"},
			cmdOK("AttachChromeExtension", "OpenChromeExtension", "AgentFile", "AgentDevtools"),
			originRefusals("PilotWS", "VaultWSS", "DeeperFTP", "ApexFTP", "WSSApexIdeographicDot"),
			nonHTTPZoneListen,
		), nil},
		{"Schemes", "OtherSchemeHostCheckRemoved", originFlagSource, originFlagPred, "func protectedExtra(n string, p *protect.Origins) bool {\n\tu, err := url.Parse(n)\n\tif err != nil || u.Scheme == \"\" {\n\t\treturn true\n\t}\n\tswitch strings.ToLower(u.Scheme) {\n\tcase \"http\", \"https\":\n\t\treturn !browser.IsSevenOrigin(n, nil) && p.Loaded(n)\n\tdefault:\n\t\treturn false\n\t}\n}\n", slices.Concat(nonHTTPZone, nonHTTPZoneListen), nil},
		{"Schemes", "SchemeFromProtect", originFlagSource, originFlagPred, "func protectedExtra(n string, p *protect.Origins) bool {\n\tswitch protect.Scheme(n) {\n\tcase \"http\", \"https\":\n\t\treturn !browser.IsSevenOrigin(n, nil) && p.Loaded(n)\n\tdefault:\n\t\th := protect.Host(n)\n\t\treturn h != \"\" && p.URL(\"https://\"+h)\n\t}\n}\n", originRefusals("UnparseableZone", "VaultPort99999", "FileZonePort", "WSPort99999", "WSSUnparseableZone", "WSSUnparseableZoneSep", "FileUnparseableZone", "EmptyHostWS"), nil},
		{"Schemes", "AuthorityHostSkipped", originFlagSource, originFlagPred, "func protectedExtra(n string, p *protect.Origins) bool {\n\tu, err := url.Parse(n)\n\tif err != nil || u.Scheme == \"\" {\n\t\treturn true\n\t}\n\tswitch strings.ToLower(u.Scheme) {\n\tcase \"http\", \"https\":\n\t\treturn !browser.IsSevenOrigin(n, nil) && p.Loaded(n)\n\tdefault:\n\t\th := protect.Host(n)\n\t\treturn h != \"\" && p.URL(\"https://\"+h)\n\t}\n}\n", originRefusals("FileZonePort", "WSPort99999", "WSSUnparseableZone", "WSSUnparseableZoneSep", "FileUnparseableZone", "EmptyHostWS"), nil},
		{"ZoneHost", "ZoneHostCheckRemoved", originFlagSource, originFlagPred, "func protectedExtra(n string, p *protect.Origins) bool {\n\tu, err := url.Parse(n)\n\tif err != nil || u.Scheme == \"\" {\n\t\treturn true\n\t}\n\tswitch strings.ToLower(u.Scheme) {\n\tcase \"http\", \"https\":\n\t\treturn !browser.IsSevenOrigin(n, nil) && p.Loaded(n)\n\tdefault:\n\t\th := protect.Host(n)\n\t\tif h == \"\" {\n\t\t\th = authorityHost(u)\n\t\t}\n\t\tif h == \"\" {\n\t\t\treturn true\n\t\t}\n\t\treturn p.URL(\"https://\" + h)\n\t}\n}\n", originRefusals("WSSUnparseableZone", "WSSUnparseableZoneSep", "FileUnparseableZone"), nil},
		{"ZoneHost", "ZoneHostSepFoldRemoved", zoneHostSource, zoneHostFold, "", originRefusals("WSSUnparseableZoneSep"), nil},
		{"Extras", "ProjectionLoaded", originFlagSource, originFlagPred, "func protectedExtra(n string, p *protect.Origins) bool {\n\tu, err := url.Parse(n)\n\tif err != nil || u.Scheme == \"\" {\n\t\treturn true\n\t}\n\tswitch strings.ToLower(u.Scheme) {\n\tcase \"http\", \"https\":\n\t\treturn !browser.IsSevenOrigin(n, nil) && p.Loaded(n)\n\tdefault:\n\t\th := protect.Host(n)\n\t\tif h == \"\" {\n\t\t\th = authorityHost(u)\n\t\t}\n\t\tif h == \"\" {\n\t\t\treturn true\n\t\t}\n\t\treturn p.Loaded(\"https://\" + h)\n\t}\n}\n", []string{originFlagAccept + "DevtoolsOpaqueHost"}, nil},
		{"Extras", "AppendDropped", originFlagSource, originFlagAppend, "\t\t_ = n\n", []string{
			originFlagAccept + "Localhost", originFlagAccept + "NonSliccy", originFlagAccept + "ChromeExtension", originFlagAccept + "FileLocalhost", originFlagAccept + "Devtools", originFlagAccept + "DevtoolsOpaqueHost", originFlagAccept + "WSNumericLabel", originFlagAccept + "WSNonZone", originFlagAccept + "PlacementMarker", originFlagAccept + "OwnerAdmission", originFlagAccept + "BiosLiveCheck", originFlagAccept + "EvaluateResultUnmarked", "TestOriginFlagAdmitsAccepted",
		}, nil},
		{"CommandOrder", "CdpCheckAfterListen", originFlagCDP, originFlagCDPCheck, "\t_ = signal.BlankOrigin\n", originListen("CdpPilot", "CdpVault", "CdpDeeper", "CdpApex", "CdpVaultWSS"), nil},
		{"CommandOrder", "AttachCheckAfterPick", originFlagAttach, originFlagAttachCheck, "\tif !f.noKernel {\n\t\tf.kernelPort = kernelin.Pick(f.kernelPort, status)\n\t}\n\torigins, err := signal.NewOrigins(f.origins)\n\tif err != nil {\n\t\treturn fail(err, 2)\n\t}\n", originListen("AttachBeforePick"), nil},
	}
}

func pageFlagCatches(t *testing.T, group string) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	sources := map[string]string{
		pageFlagSource:   read(pageFlagSource),
		pageFlagDefault:  read(pageFlagDefault),
		originFlagSource: read(originFlagSource),
		originFlagCDP:    read(originFlagCDP),
		originFlagAttach: read(originFlagAttach),
		zoneHostSource:   read(zoneHostSource),
	}
	var mutants []pageFlagMutant
	for _, m := range pageFlagMutants() {
		if m.group == group {
			mutants = append(mutants, m)
		}
	}
	if len(mutants) == 0 {
		t.Fatalf("no mutant is in group %s", group)
	}
	baseline := pageFlagBaseline(t)
	for _, m := range mutants {
		t.Run(m.name, func(t *testing.T) {
			source := sources[m.file]
			if n := strings.Count(source, m.from); n != 1 {
				t.Fatalf("mutant %s anchor appears %d times in %s", m.name, n, m.file)
			}
			allowed := map[string]bool{}
			for _, row := range m.named {
				if _, ok := baseline[row]; !ok {
					t.Fatalf("mutant %s names %s, which is not a baseline row", m.name, row)
				}
				allowed[row] = true
			}
			for _, row := range m.incidental {
				if _, ok := baseline[row]; !ok {
					t.Fatalf("mutant %s incidental %s is not a baseline row", m.name, row)
				}
				allowed[row] = true
			}
			got, unclean := pageFlagResults(t, map[string]string{m.file: strings.Replace(source, m.from, m.to, 1)})
			var actualRed, missingNamed, unexpected, missingLeaf, skipped []string
			for row := range baseline {
				result, ok := got[row]
				if !ok {
					missingLeaf = append(missingLeaf, row)
					if !allowed[row] {
						unexpected = append(unexpected, row+"<missing>")
					}
					continue
				}
				if result == "SKIP" {
					skipped = append(skipped, row)
					if !allowed[row] {
						unexpected = append(unexpected, row+"<SKIP>")
					}
					continue
				}
				if result == "FAIL" {
					actualRed = append(actualRed, row)
					if !allowed[row] {
						unexpected = append(unexpected, row)
					}
				}
			}
			for row, result := range got {
				if _, ok := baseline[row]; !ok {
					t.Errorf("mutant %s: row %s (%s) is not a baseline row", m.name, row, result)
				}
			}
			for _, row := range m.named {
				if got[row] != "FAIL" {
					missingNamed = append(missingNamed, row)
				}
			}
			slices.Sort(actualRed)
			slices.Sort(missingNamed)
			slices.Sort(unexpected)
			slices.Sort(missingLeaf)
			slices.Sort(skipped)
			if unclean != "" || len(missingNamed) > 0 || len(unexpected) > 0 || len(missingLeaf) > 0 || len(skipped) > 0 {
				t.Errorf("mutant %s: unclean=%q named want FAIL missing=%q; unexpected=%q; missing leaf=%q; SKIP=%q; actual FAIL=%q; named=%q; incidental=%q",
					m.name, unclean, missingNamed, unexpected, missingLeaf, skipped, actualRed, m.named, m.incidental)
			}
		})
	}
}

func TestPageFlagCatchesPageGuardMutants(t *testing.T) {
	pageFlagCatches(t, "PageGuard")
}

func TestPageFlagCatchesExtrasMutants(t *testing.T) {
	pageFlagCatches(t, "Extras")
}

func TestPageFlagCatchesSchemesMutants(t *testing.T) {
	pageFlagCatches(t, "Schemes")
}

func TestPageFlagCatchesZoneHostMutants(t *testing.T) {
	pageFlagCatches(t, "ZoneHost")
}

func TestPageFlagCatchesCommandOrderMutants(t *testing.T) {
	pageFlagCatches(t, "CommandOrder")
}

func TestPageFlagMutantGroupsCoverEveryMutant(t *testing.T) {
	data, err := os.ReadFile("pageflag_test.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	group := map[string]string{}
	count := map[string]int{}
	for _, m := range pageFlagMutants() {
		if prev, dup := group[m.name]; dup {
			t.Errorf("mutant %s is in group %s and in group %s", m.name, prev, m.group)
		}
		group[m.name] = m.group
		count[m.group]++
	}
	if len(group) != pageFlagMutantCount {
		t.Errorf("%d distinct mutants, want %d", len(group), pageFlagMutantCount)
	}
	for g, n := range count {
		if c := strings.Count(source, "\tpageFlagCatches(t, \""+g+"\")\n"); c != 1 {
			t.Errorf("group %s (%d mutants) is run by %d tests, want 1", g, n, c)
		}
	}
	if c := strings.Count(source, "\tpageFlagCatches(t, \""); c != len(count) {
		t.Errorf("%d tests run a mutant group, but the mutants are in %d groups", c, len(count))
	}
}
