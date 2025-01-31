package run

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/fatih/color"
	"github.com/rs/zerolog/log"
	"github.com/saucelabs/saucectl/cli/command"
	"github.com/saucelabs/saucectl/cli/flags"
	"github.com/saucelabs/saucectl/cli/version"
	"github.com/saucelabs/saucectl/internal/appstore"
	"github.com/saucelabs/saucectl/internal/config"
	"github.com/saucelabs/saucectl/internal/credentials"
	"github.com/saucelabs/saucectl/internal/espresso"
	"github.com/saucelabs/saucectl/internal/rdc"
	"github.com/saucelabs/saucectl/internal/region"
	"github.com/saucelabs/saucectl/internal/resto"
	"github.com/saucelabs/saucectl/internal/saucecloud"
	"github.com/saucelabs/saucectl/internal/sentry"
	"github.com/saucelabs/saucectl/internal/testcomposer"
	"github.com/spf13/cobra"
)

// espFlags contains all espresso related flags that are set when 'run' is invoked.
var espFlags = espressoFlags{}

type espressoFlags struct {
	Name        string
	App         string
	TestApp     string
	TestOptions espresso.TestOptions
	Emulator    flags.Emulator
	Device      flags.Device
}

// NewEspressoCmd creates the 'run' command for espresso.
func NewEspressoCmd(cli *command.SauceCtlCli) *cobra.Command {
	cmd := &cobra.Command{
		Use:              "espresso",
		Short:            "Run espresso tests",
		Hidden:           true, // TODO reveal command once ready
		TraverseChildren: true,
		Run: func(cmd *cobra.Command, args []string) {
			exitCode, err := runEspressoCmd(cmd, cli, args)
			if err != nil {
				log.Err(err).Msg("failed to execute run command")
				sentry.CaptureError(err, sentry.Scope{
					Username:   credentials.Get().Username,
					ConfigFile: gFlags.cfgFilePath,
				})
			}
			os.Exit(exitCode)
		},
	}

	f := cmd.Flags()
	f.StringVar(&espFlags.Name, "name", "", "Sets the name of job as it will appear on Sauce Labs")
	f.StringVar(&espFlags.App, "app", "", "Specifies the app under test")
	f.StringVar(&espFlags.TestApp, "testApp", "", "Specifies the test app")

	// Test Options
	f.StringSliceVar(&espFlags.TestOptions.Class, "testOptions.class", []string{}, "Include classes")
	f.StringSliceVar(&espFlags.TestOptions.NotClass, "testOptions.notClass", []string{}, "Exclude classes")
	f.StringVar(&espFlags.TestOptions.Package, "testOptions.package", "", "Include package")
	f.StringVar(&espFlags.TestOptions.Size, "testOptions.size", "", "Include tests based on size")
	f.StringVar(&espFlags.TestOptions.Annotation, "testOptions.annotation", "", "Include tests based on the annotation")
	f.IntVar(&espFlags.TestOptions.ShardIndex, "testOptions.shardIndex", 0, "The shard index for this particular run")
	f.IntVar(&espFlags.TestOptions.NumShards, "testOptions.numShards", 0, "Total number of shards")

	// Emulators and Devices
	f.Var(&espFlags.Emulator, "emulator", "Specifies the emulator to use for testing")
	f.Var(&espFlags.Device, "device", "Specifies the device to use for testing")

	return cmd
}

// runEspressoCmd runs the espresso 'run' command.
func runEspressoCmd(cmd *cobra.Command, cli *command.SauceCtlCli, args []string) (int, error) {
	println("Running version", version.Version)
	checkForUpdates()
	go awaitGlobalTimeout()

	creds := credentials.Get()
	if !creds.IsValid() {
		color.Red("\nSauceCTL requires a valid Sauce Labs account!\n\n")
		fmt.Println(`Set up your credentials by running:
> saucectl configure`)
		println()
		return 1, fmt.Errorf("no credentials set")
	}

	if gFlags.cfgLogDir == defaultLogFir {
		pwd, _ := os.Getwd()
		gFlags.cfgLogDir = filepath.Join(pwd, "logs")
	}
	cli.LogDir = gFlags.cfgLogDir
	log.Info().Str("config", gFlags.cfgFilePath).Msg("Reading config file")

	d, err := config.Describe(gFlags.cfgFilePath)
	if err != nil {
		return 1, err
	}

	tc := testcomposer.Client{
		HTTPClient:  &http.Client{Timeout: testComposerTimeout},
		URL:         "", // updated later once region is determined
		Credentials: creds,
	}

	rs := resto.Client{
		HTTPClient: &http.Client{Timeout: restoTimeout},
		URL:        "", // updated later once region is determined
		Username:   creds.Username,
		AccessKey:  creds.AccessKey,
	}

	rc := rdc.Client{
		HTTPClient: &http.Client{
			Timeout: rdcTimeout,
		},
		Username:  creds.Username,
		AccessKey: creds.AccessKey,
	}

	as := appstore.New("", creds.Username, creds.AccessKey, appStoreTimeout)

	if d.Kind == config.KindEspresso && d.APIVersion == config.VersionV1Alpha {
		return runEspresso(cmd, tc, rs, rc, as)
	}

	return 1, errors.New("unknown framework configuration")
}

func runEspresso(cmd *cobra.Command, tc testcomposer.Client, rs resto.Client, rc rdc.Client, as *appstore.AppStore) (int, error) {
	p, err := espresso.FromFile(gFlags.cfgFilePath)
	if err != nil {
		return 1, err
	}
	p.Sauce.Metadata.ExpandEnv()
	applyDefaultValues(&p.Sauce)
	overrideCliParameters(cmd, &p.Sauce, &p.Artifacts)
	applyEspressoFlags(&p)

	regio := region.FromString(p.Sauce.Region)
	if regio == region.None {
		log.Error().Str("region", gFlags.regionFlag).Msg("Unable to determine sauce region.")
		return 1, errors.New("no sauce region set")
	}

	err = espresso.Validate(p)
	if err != nil {
		return 1, err
	}

	if cmd.Flags().Lookup("suite").Changed {
		if err := filterEspressoSuite(&p); err != nil {
			return 1, err
		}
	}

	tc.URL = regio.APIBaseURL()
	rs.URL = regio.APIBaseURL()
	as.URL = regio.APIBaseURL()
	rc.URL = regio.APIBaseURL()

	rs.ArtifactConfig = p.Artifacts.Download
	rc.ArtifactConfig = p.Artifacts.Download

	return runEspressoInCloud(p, regio, tc, rs, rc, as)
}

func runEspressoInCloud(p espresso.Project, regio region.Region, tc testcomposer.Client, rs resto.Client, rc rdc.Client, as *appstore.AppStore) (int, error) {
	log.Info().Msg("Running Espresso in Sauce Labs")
	printTestEnv("sauce")

	r := saucecloud.EspressoRunner{
		Project: p,
		CloudRunner: saucecloud.CloudRunner{
			ProjectUploader:       as,
			JobStarter:            &tc,
			JobReader:             &rs,
			RDCJobReader:          &rc,
			JobStopper:            &rs,
			JobWriter:             &tc,
			CCYReader:             &rs,
			TunnelService:         &rs,
			Region:                regio,
			ShowConsoleLog:        false,
			ArtifactDownloader:    &rs,
			RDCArtifactDownloader: &rc,
			DryRun:                gFlags.dryRun,
		},
	}

	return r.RunProject()
}

func filterEspressoSuite(c *espresso.Project) error {
	for _, s := range c.Suites {
		if s.Name == gFlags.suiteName {
			c.Suites = []espresso.Suite{s}
			return nil
		}
	}
	return fmt.Errorf("suite name '%s' is invalid", gFlags.suiteName)
}

func applyEspressoFlags(p *espresso.Project) {
	if espFlags.App != "" {
		p.Espresso.App = espFlags.App
	}
	if espFlags.TestApp != "" {
		p.Espresso.TestApp = espFlags.TestApp
	}

	// No name, no adhoc suite.
	if espFlags.Name != "" {
		setAdhocSuite(p)
	}
}

func setAdhocSuite(p *espresso.Project) {
	var dd []config.Device
	if espFlags.Device.Changed {
		dd = append(dd, espFlags.Device.Device)
	}

	var ee []config.Emulator
	if espFlags.Emulator.Changed {
		ee = append(ee, espFlags.Emulator.Emulator)
	}

	s := espresso.Suite{
		Name:        espFlags.Name,
		Devices:     dd,
		Emulators:   ee,
		TestOptions: espFlags.TestOptions,
	}
	p.Suites = []espresso.Suite{s}
}
