package init

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/AlecAivazis/survey/v2/terminal"

	"github.com/AlecAivazis/survey/v2"
	"github.com/saucelabs/saucectl/internal/config"
	"github.com/saucelabs/saucectl/internal/credentials"
	"github.com/saucelabs/saucectl/internal/devices"
	"github.com/saucelabs/saucectl/internal/framework"
	"github.com/saucelabs/saucectl/internal/rdc"
	"github.com/saucelabs/saucectl/internal/region"
	"github.com/saucelabs/saucectl/internal/resto"
	"github.com/saucelabs/saucectl/internal/testcomposer"
	"github.com/saucelabs/saucectl/internal/vmd"
)

var androidDevicesPatterns = []string{
	"Amazon Kindle Fire .*", "Google Pixel .*", "HTC .*", "Huawei .*",
	"LG .*", "Motorola .*", "OnePlus .*", "Samsung .*", "Sony .*",
}

var iOSDevicesPatterns = []string{"iPad .*", "iPhone .*"}

type initiator struct {
	stdio        terminal.Stdio
	infoReader   framework.MetadataService
	deviceReader devices.Reader
	vmdReader    vmd.Reader

	frameworks        []string
	frameworkMetadata []framework.Metadata
}

// newInitiator creates a new initiator instance.
func newInitiator(stdio terminal.Stdio, creds credentials.Credentials, regio string) *initiator {
	r := region.FromString(regio)
	tc := testcomposer.Client{
		HTTPClient:  &http.Client{Timeout: testComposerTimeout},
		URL:         r.APIBaseURL(),
		Credentials: creds,
	}

	rc := rdc.Client{
		HTTPClient: &http.Client{Timeout: rdcTimeout},
		URL:        r.APIBaseURL(),
		Username:   creds.Username,
		AccessKey:  creds.AccessKey,
	}

	rs := resto.Client{
		HTTPClient: &http.Client{Timeout: restoTimeout},
		URL:        r.APIBaseURL(),
		Username:   creds.Username,
		AccessKey:  creds.AccessKey,
	}

	return &initiator{
		stdio:        stdio,
		infoReader:   &tc,
		deviceReader: &rc,
		vmdReader:    &rs,
	}
}

func isNativeFramework(framework string) bool {
	return framework == config.KindEspresso || framework == config.KindXcuitest
}

func needsApps(framework string) bool {
	return isNativeFramework(framework)
}

func needsCypressJson(framework string) bool {
	return framework == config.KindCypress
}

func needsDevice(framework string) bool {
	return isNativeFramework(framework)
}

func needsEmulator(framework string) bool {
	return framework == config.KindEspresso
}

func needsPlatform(framework string) bool {
	return !isNativeFramework(framework)
}

func needsRootDir(framework string) bool {
	return !isNativeFramework(framework)
}

func needsVersion(framework string) bool {
	return !isNativeFramework(framework)
}

func (ini *initiator) configure() (*initConfig, error) {
	cfg := &initConfig{}

	err := ini.askFramework(cfg)
	if err != nil {
		return &initConfig{}, err
	}

	frameworkMetadatas, err := ini.infoReader.Versions(context.Background(), cfg.frameworkName)
	if err != nil {
		return &initConfig{}, err
	}

	if needsVersion(cfg.frameworkName) {
		err = ini.askVersion(cfg, frameworkMetadatas)
		if err != nil {
			return &initConfig{}, err
		}
	}

	if needsCypressJson(cfg.frameworkName) {
		err = ini.askFile("Cypress configuration file:", extValidator(cfg.frameworkName), completeBasic, &cfg.cypressJson)
		if err != nil {
			return &initConfig{}, err
		}
	}

	if needsPlatform(cfg.frameworkName) {
		err = ini.askPlatform(cfg, frameworkMetadatas)
		if err != nil {
			return &initConfig{}, err
		}
	}

	if needsApps(cfg.frameworkName) {
		err = ini.askFile("Application to test:", extValidator(cfg.frameworkName), completeBasic, &cfg.app)
		if err != nil {
			return &initConfig{}, err
		}

		err = ini.askFile("Test application:", extValidator(cfg.frameworkName), completeBasic, &cfg.testApp)
		if err != nil {
			return &initConfig{}, err
		}
	}

	if needsDevice(cfg.frameworkName) {
		patterns := androidDevicesPatterns
		if cfg.frameworkName == config.KindXcuitest {
			patterns = iOSDevicesPatterns
		}
		if err != nil {
			return &initConfig{}, err
		}

		err = ini.askDevice(cfg, patterns)
		if err != nil {
			return &initConfig{}, err
		}

	}

	if needsEmulator(cfg.frameworkName) {
		vmdKind := vmd.AndroidEmulator
		virtualDevices, err := ini.vmdReader.GetVirtualDevices(context.Background(), vmdKind)

		if err != nil {
			return &initConfig{}, err
		}
		err = ini.askEmulator(cfg, virtualDevices)
		if err != nil {
			return &initConfig{}, err
		}
	}

	err = ini.askDownloadWhen(cfg)
	if err != nil {
		return &initConfig{}, err
	}
	return cfg, nil
}

func askCredentials(stdio terminal.Stdio) (credentials.Credentials, error) {
	creds := credentials.Credentials{}
	q := &survey.Input{Message: "SauceLabs username:"}

	err := survey.AskOne(q, &creds.Username,
		survey.WithValidator(survey.Required),
		survey.WithShowCursor(true),
		survey.WithStdio(stdio.In, stdio.Out, stdio.Err))
	if err != nil {
		return creds, err
	}

	q = &survey.Input{Message: "SauceLabs access key:"}
	err = survey.AskOne(q, &creds.AccessKey,
		survey.WithValidator(survey.Required),
		survey.WithShowCursor(true),
		survey.WithStdio(stdio.In, stdio.Out, stdio.Err))
	if err != nil {
		return creds, err
	}
	return creds, nil
}

func askRegion(stdio terminal.Stdio) (string, error) {
	var r string
	p := &survey.Select{
		Message: "Select region:",
		Options: []string{region.USWest1.String(), region.EUCentral1.String()},
		Default: region.USWest1.String(),
	}

	err := survey.AskOne(p, &r, survey.WithStdio(stdio.In, stdio.Out, stdio.Err))
	if err != nil {
		return "", err
	}
	return r, nil
}

func (ini *initiator) askFramework(cfg *initConfig) error {
	values, err := ini.infoReader.Frameworks(context.Background())
	if err != nil {
		return err
	}

	var frameworks []string
	for _, f := range values {
		frameworks = append(frameworks, f.Name)
	}

	p := &survey.Select{
		Message: "Select framework:",
		Options: frameworks,
	}

	err = survey.AskOne(p, &cfg.frameworkName, survey.WithStdio(ini.stdio.In, ini.stdio.Out, ini.stdio.Err))
	if cfg.frameworkName == "" {
		return errors.New("interrupting configuration")
	}
	cfg.frameworkName = strings.ToLower(cfg.frameworkName)
	return err
}

type completor func(string) []string

/* When translation */
var whenStrings = []string{
	"when tests are failing",
	"when tests are passing",
	"never",
	"always",
}
var mapWhen = map[string]config.When{
	"when tests are failing": config.WhenFail,
	"when tests are passing": config.WhenPass,
	"never":                  config.WhenNever,
	"always":                 config.WhenAlways,
}

func (ini *initiator) askDownloadWhen(cfg *initConfig) error {
	q := &survey.Select{
		Message: "Download artifacts:",
		Default: whenStrings[0],
		Options: whenStrings,
	}
	q.WithStdio(ini.stdio)

	var when string
	err := survey.AskOne(q, &when,
		survey.WithShowCursor(true),
		survey.WithValidator(survey.Required),
		survey.WithStdio(ini.stdio.In, ini.stdio.Out, ini.stdio.Err))
	if err != nil {
		return err
	}
	cfg.artifactWhen = mapWhen[when]
	return nil
}

func (ini *initiator) askDevice(cfg *initConfig, suggestions []string) error {
	q := &survey.Select{
		Message: "Select device pattern:",
		Options: suggestions,
	}
	err := survey.AskOne(q, &cfg.device.Name,
		survey.WithShowCursor(true),
		survey.WithStdio(ini.stdio.In, ini.stdio.Out, ini.stdio.Err))
	if err != nil {
		return err
	}
	return nil
}

func (ini *initiator) askEmulator(cfg *initConfig, vmds []vmd.VirtualDevice) error {
	var vmdNames []string
	for _, v := range vmds {
		vmdNames = append(vmdNames, v.Name)
	}
	q := &survey.Select{
		Message: "Select emulator:",
		Options: uniqSorted(vmdNames),
	}
	err := survey.AskOne(q, &cfg.emulator.Name,
		survey.WithShowCursor(true),
		survey.WithStdio(ini.stdio.In, ini.stdio.Out, ini.stdio.Err))
	if err != nil {
		return err
	}
	return nil
}

func metaToVersions(metadatas []framework.Metadata) []string {
	var versions []string
	for _, v := range metadatas {
		versions = append(versions, v.FrameworkVersion)
	}
	return versions
}

func metaToPlatforms(metadatas []framework.Metadata, version string) []string {
	var platforms []string
	for _, m := range metadatas {
		if m.FrameworkVersion == version {
			for _, p := range m.Platforms {
				platforms = append(platforms, p.PlatformName)
			}
			if m.DockerImage != "" {
				platforms = append(platforms, "docker")
			}
		}
	}
	return platforms
}

func metaToBrowsers(metadatas []framework.Metadata, frameworkName, frameworkVersion, platformName string) []string {
	if platformName == "docker" {
		return dockerBrowsers(frameworkName)
	}

	// It's not optimum to have double iteration, but since the set it pretty small this will be insignificant.
	// It's helping for readability.
	for _, v := range metadatas {
		for _, p := range v.Platforms {
			if v.FrameworkVersion == frameworkVersion && p.PlatformName == platformName {
				return p.BrowserNames
			}
		}
	}
	return []string{}
}

func dockerBrowsers(framework string) []string {
	switch framework {
	case "playwright":
		return []string{"chromium", "firefox"}
	default:
		return []string{"chrome", "firefox"}
	}
}

func (ini *initiator) askPlatform(cfg *initConfig, metadatas []framework.Metadata) error {
	platformChoices := metaToPlatforms(metadatas, cfg.frameworkVersion)

	q := &survey.Select{
		Message: "Select platform:",
		Options: platformChoices,
	}
	err := survey.AskOne(q, &cfg.platformName,
		survey.WithShowCursor(true),
		survey.WithValidator(survey.Required),
		survey.WithStdio(ini.stdio.In, ini.stdio.Out, ini.stdio.Err))
	if err != nil {
		return err
	}

	// Select browser
	browserChoices := metaToBrowsers(metadatas, cfg.frameworkName, cfg.frameworkVersion, cfg.platformName)
	q = &survey.Select{
		Message: "Select Browser:",
		Options: browserChoices,
	}
	err = survey.AskOne(q, &cfg.browserName,
		survey.WithShowCursor(true),
		survey.WithValidator(survey.Required),
		survey.WithStdio(ini.stdio.In, ini.stdio.Out, ini.stdio.Err))
	if err != nil {
		return err
	}

	cfg.mode = "sauce"
	if cfg.platformName == "docker" {
		cfg.platformName = ""
		cfg.mode = "docker"
	}
	return nil
}

func (ini *initiator) askVersion(cfg *initConfig, metadatas []framework.Metadata) error {
	versions := metaToVersions(metadatas)

	q := &survey.Select{
		Message: fmt.Sprintf("Select %s version:", cfg.frameworkName),
		Options: versions,
	}

	err := survey.AskOne(q, &cfg.frameworkVersion,
		survey.WithShowCursor(true),
		survey.WithValidator(survey.Required),
		survey.WithStdio(ini.stdio.In, ini.stdio.Out, ini.stdio.Err))
	if err != nil {
		return err
	}
	return nil
}

func (ini *initiator) askFile(message string, val survey.Validator, comp completor, targetValue *string) error {
	q := &survey.Input{
		Message: message,
		Suggest: comp,
	}

	if err := survey.AskOne(q, targetValue,
		survey.WithShowCursor(true),
		survey.WithValidator(survey.Required),
		survey.WithValidator(val),
		survey.WithStdio(ini.stdio.In, ini.stdio.Out, ini.stdio.Err)); err != nil {
		return err
	}
	return nil
}