package init

import (
	"fmt"
	"os"
	"strings"

	"github.com/AlecAivazis/survey/v2/terminal"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/saucelabs/saucectl/cli/command"
	"github.com/saucelabs/saucectl/internal/config"
	"github.com/saucelabs/saucectl/internal/mocks"
	"github.com/saucelabs/saucectl/internal/sentry"
)

var (
	initUse     = "init"
	initShort   = "bootstrap project"
	initLong    = "bootstrap an existing project for Sauce Labs"
	initExample = "saucectl init"

	frameworkName = ""
)

// Command creates the `run` command
func Command(cli *command.SauceCtlCli) *cobra.Command {
	cmd := &cobra.Command{
		Use:     initUse,
		Short:   initShort,
		Long:    initLong,
		Example: initExample,
		Run: func(cmd *cobra.Command, args []string) {
			log.Info().Msg("Start Init Command")
			err := Run(cmd, cli, args)
			if err != nil {
				log.Err(err).Msg("failed to execute init command")
				sentry.CaptureError(err, sentry.Scope{})
				os.Exit(1)
			}
		},
	}
	cmd.Flags().StringVarP(&frameworkName, "framework", "f", "", "framework to init")
	return cmd
}

type FrameworkInfoReader interface {
	Frameworks() ([]string, error)
	Versions(frameworkName, region string) ([]string, error)
	Platforms(frameworkName, region, frameworkVersion string) ([]string, error)
	Browsers(frameworkName, region, frameworkVersion, platformName string) ([]string, error)
}

type initiator struct {
	stdio      terminal.Stdio
	infoReader FrameworkInfoReader

	frameworkName    string
	frameworkVersion string
	platformName     string
	mode             string
	browserName      string
	region           string
	artifactWhen     config.When
	device           config.Device
	emulator         config.Emulator
}

// Run runs the command
func Run(cmd *cobra.Command, cli *command.SauceCtlCli, args []string) error {
	// FIXME: Provision using API
	ini := initiator{
		stdio:      terminal.Stdio{In: os.Stdin, Out: os.Stdout, Err: os.Stderr},
		infoReader: &mocks.FakeFrameworkInfoReader{},
	}

	err := ini.askFramework()
	if err != nil {
		return err
	}

	switch strings.ToLower(ini.frameworkName) {
	case "espresso":
		return configureEspresso(ini)
	case "xcuitest":
		return configureXCUITest(ini)
	case "cypress":
		return configureCypress(ini)
	case "testcafe":
		return configureTestcafe(ini)
	case "playwright":
		return configurePlaywright(ini)
	case "puppeteer":
		return configurePuppeteer(ini)
	case "":
		return fmt.Errorf("interrupting configuration")
	default:
		return fmt.Errorf("%s: not implemented", strings.ToLower(ini.frameworkName))
	}
}
