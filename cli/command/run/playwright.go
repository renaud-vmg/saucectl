package run

import (
	"errors"
	"fmt"
	"github.com/rs/zerolog/log"
	"github.com/saucelabs/saucectl/internal/appstore"
	"github.com/saucelabs/saucectl/internal/docker"
	"github.com/saucelabs/saucectl/internal/playwright"
	"github.com/saucelabs/saucectl/internal/region"
	"github.com/saucelabs/saucectl/internal/resto"
	"github.com/saucelabs/saucectl/internal/saucecloud"
	"github.com/saucelabs/saucectl/internal/testcomposer"
	"github.com/spf13/cobra"
)

func runPlaywright(cmd *cobra.Command, tc testcomposer.Client, rs resto.Client, as *appstore.AppStore) (int, error) {
	p, err := playwright.FromFile(gFlags.cfgFilePath)
	if err != nil {
		return 1, err
	}

	p.Sauce.Metadata.ExpandEnv()
	applyDefaultValues(&p.Sauce)
	overrideCliParameters(cmd, &p.Sauce, &p.Artifacts)

	// Merge env from CLI args and job config. CLI args take precedence.
	for k, v := range gFlags.env {
		for _, s := range p.Suites {
			if s.Env == nil {
				s.Env = map[string]string{}
			}
			s.Env[k] = v
		}
	}

	if gFlags.showConsoleLog {
		p.ShowConsoleLog = true
	}
	if gFlags.runnerVersion != "" {
		p.RunnerVersion = gFlags.runnerVersion
	}
	if cmd.Flags().Lookup("suite").Changed {
		if err := filterPlaywrightSuite(&p); err != nil {
			return 1, err
		}
	}
	if p.Defaults.Mode == "" {
		p.Defaults.Mode = "sauce"
	}
	for i, s := range p.Suites {
		if s.Mode == "" {
			s.Mode = p.Defaults.Mode
		}
		p.Suites[i] = s
	}
	if gFlags.testEnv != "" {
		for i, s := range p.Suites {
			s.Mode = gFlags.testEnv
			p.Suites[i] = s
		}
	}

	regio := region.FromString(p.Sauce.Region)
	if regio == region.None {
		log.Error().Str("region", gFlags.regionFlag).Msg("Unable to determine sauce region.")
		return 1, errors.New("no sauce region set")
	}

	tc.URL = regio.APIBaseURL()
	rs.URL = regio.APIBaseURL()
	as.URL = regio.APIBaseURL()

	rs.ArtifactConfig = p.Artifacts.Download

	dockerProject, sauceProject := playwright.SplitSuites(p)
	if len(dockerProject.Suites) != 0 {
		exitCode, err := runPlaywrightInDocker(dockerProject, tc, rs)
		if err != nil || exitCode != 0 {
			return exitCode, err
		}
	}
	if len(sauceProject.Suites) != 0 {
		return runPlaywrightInSauce(sauceProject, regio, tc, rs, as)
	}

	return 0, nil
}

func runPlaywrightInDocker(p playwright.Project, testco testcomposer.Client, rs resto.Client) (int, error) {
	log.Info().Msg("Running Playwright in Docker")
	printTestEnv("docker")

	cd, err := docker.NewPlaywright(p, &testco, &testco, &rs)
	if err != nil {
		return 1, err
	}
	return cd.RunProject()
}

func runPlaywrightInSauce(p playwright.Project, regio region.Region, tc testcomposer.Client, rs resto.Client, as *appstore.AppStore) (int, error) {
	log.Info().Msg("Running Playwright in Sauce Labs")
	printTestEnv("sauce")

	r := saucecloud.PlaywrightRunner{
		Project: p,
		CloudRunner: saucecloud.CloudRunner{
			ProjectUploader:    as,
			JobStarter:         &tc,
			JobReader:          &rs,
			JobStopper:         &rs,
			JobWriter:          &tc,
			CCYReader:          &rs,
			TunnelService:      &rs,
			Region:             regio,
			ShowConsoleLog:     p.ShowConsoleLog,
			ArtifactDownloader: &rs,
			DryRun:             gFlags.dryRun,
		},
	}
	return r.RunProject()
}

func filterPlaywrightSuite(c *playwright.Project) error {
	for _, s := range c.Suites {
		if s.Name == gFlags.suiteName {
			c.Suites = []playwright.Suite{s}
			return nil
		}
	}
	return fmt.Errorf("suite name '%s' is invalid", gFlags.suiteName)
}

