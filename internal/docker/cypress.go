package docker

import (
	"context"
	"fmt"
	"github.com/rs/zerolog/log"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"

	"github.com/saucelabs/saucectl/cli/command"
	"github.com/saucelabs/saucectl/internal/cypress"
	"github.com/saucelabs/saucectl/internal/jsonio"
)

// CypressRunner represents the docker implementation of a test runner.
type CypressRunner struct {
	ContainerRunner
	Project cypress.Project
}

// NewCypress creates a new CypressRunner instance.
func NewCypress(c cypress.Project, cli *command.SauceCtlCli) (*CypressRunner, error) {
	r := CypressRunner{
		Project: c,
		ContainerRunner: ContainerRunner{
			Ctx:             context.Background(),
			Cli:             cli,
			containerID:     "",
			docker:          nil,
			containerConfig: &containerConfig{},
		},
	}

	var err error
	r.docker, err = Create()
	if err != nil {
		return nil, err
	}

	return &r, nil
}

// RunProject runs the tests defined in config.Project.
func (r *CypressRunner) RunProject() (int, error) {
	if err := r.defineDockerImage(); err != nil {
		return 1, err
	}

	errorCount := 0
	for _, suite := range r.Project.Suites {
		log.Info().Msg("Setting up test environment")
		if err := r.setup(); err != nil {
			log.Err(err).Msg("Failed to setup test environment")
			return 1, err
		}

		err := r.run([]string{"npm", "test", "--", "-r", r.containerConfig.sauceRunnerConfigPath, "-s", suite.Name},
			suite.Config.Env)
		if err != nil {
			errorCount++
		}
	}
	if errorCount > 0 {
		log.Error().Msgf("%d suite(s) failed", errorCount)
	}
	return errorCount, nil
}

// defineDockerImage defines docker image value if not already set.
func (r *CypressRunner) defineDockerImage() error {
	// Skip availability check since custom image is being used
	if r.Project.Docker.Image.Name != "" && r.Project.Docker.Image.Tag != "" {
		log.Info().Msgf("Ignoring Cypress version for Docker, using %s:%s", r.Project.Docker.Image.Name, r.Project.Docker.Image.Tag)
		return nil
	}

	if r.Project.Cypress.Version == "" {
		return fmt.Errorf("Missing cypress version. Check available versions here: https://docs.staging.saucelabs.net/testrunner-toolkit#supported-frameworks-and-browsers")
	}

	if r.Project.Docker.Image.Name == cypress.DefaultDockerImage && r.Project.Docker.Image.Tag == "" {
		r.Project.Docker.Image.Tag = "v" + r.Project.Cypress.Version
	}
	if r.Project.Docker.Image.Name == "" {
		r.Project.Docker.Image.Name = cypress.DefaultDockerImage
		r.Project.Docker.Image.Tag = "v" + r.Project.Cypress.Version
	}
	return nil
}

// setup performs any necessary steps for a test runner to execute tests.
func (r *CypressRunner) setup() error {
	err := r.docker.ValidateDependency()
	if err != nil {
		return fmt.Errorf("please verify that docker is installed and running: %v, "+
			" follow the guide at https://docs.docker.com/get-docker/", err)
	}

	if err := r.pullImage(r.Project.Docker.Image); err != nil {
		return err
	}

	files := []string{
		r.Project.Cypress.ConfigFile,
		r.Project.Cypress.ProjectPath,
	}

	if r.Project.Cypress.EnvFile != "" {
		files = append(files, r.Project.Cypress.EnvFile)
	}

	container, err := r.docker.StartContainer(r.Ctx, files, r.Project.Docker)
	if err != nil {
		return err
	}
	r.containerID = container.ID

	pDir, err := r.docker.ProjectDir(r.Ctx, r.Project.Docker.Image.String())
	if err != nil {
		return err
	}

	tmpDir, err := ioutil.TempDir("", "saucectl")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	rcPath := filepath.Join(tmpDir, SauceRunnerConfigFile)
	if err := jsonio.WriteFile(rcPath, r.Project); err != nil {
		return err
	}

	if err := r.docker.CopyToContainer(r.Ctx, r.containerID, rcPath, pDir); err != nil {
		return err
	}
	r.containerConfig.sauceRunnerConfigPath = path.Join(pDir, SauceRunnerConfigFile)

	// running pre-exec tasks
	err = r.beforeExec(r.Project.BeforeExec)
	if err != nil {
		return err
	}
	// start port forwarding
	sockatCmd := []string{
		"socat",
		"tcp-listen:9222,reuseaddr,fork",
		"tcp:localhost:9223",
	}

	if _, _, err := r.docker.Execute(r.Ctx, r.containerID, sockatCmd, nil); err != nil {
		return err
	}

	return nil
}