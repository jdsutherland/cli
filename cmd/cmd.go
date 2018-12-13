package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	netURL "net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/exercism/cli/api"
	"github.com/exercism/cli/config"
	"github.com/exercism/cli/workspace"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

var (
	// BinaryName is the name of the app.
	// By default this is exercism, but people
	// are free to name this however they want.
	// The usage examples and help strings should reflect
	// the actual name of the binary.
	BinaryName string
	// Out is used to write to information.
	Out io.Writer
	// Err is used to write errors.
	Err io.Writer
)

const msgWelcomePleaseConfigure = `

    Welcome to Exercism!

    To get started, you need to configure the tool with your API token.
    Find your token at

        %s

    Then run the configure command:

        %s configure --token=YOUR_TOKEN

`

// Running configure without any arguments will attempt to
// set the default workspace. If the default workspace directory
// risks clobbering an existing directory, it will print an
// error message that explains how to proceed.
const msgRerunConfigure = `

    Please re-run the configure command to define where
    to download the exercises.

        %s configure
`

const msgMissingMetadata = `

    The exercise you are submitting doesn't have the necessary metadata.
    Please see https://exercism.io/cli-v1-to-v2 for instructions on how to fix it.

`

// validateUserConfig validates the presense of required user config values
func validateUserConfig(cfg *viper.Viper) error {
	if cfg.GetString("token") == "" {
		return fmt.Errorf(
			msgWelcomePleaseConfigure,
			config.SettingsURL(cfg.GetString("apibaseurl")),
			BinaryName,
		)
	}
	if cfg.GetString("workspace") == "" || cfg.GetString("apibaseurl") == "" {
		return fmt.Errorf(msgRerunConfigure, BinaryName)
	}
	return nil
}

// downloadContext represents the required context around obtaining a Solution
// payload from the API and working with the its contents.
type downloadContext struct {
	usrCfg *viper.Viper
	*downloadParams
	payload *downloadPayload
}

// newDownloadContext creates a downloadContext, making an HTTP request
// to populate the payload.
func newDownloadContext(usrCfg *viper.Viper, params *downloadParams) (*downloadContext, error) {
	if err := params.validate(); err != nil {
		return nil, errors.New("tried to download but missing a 'slug' or a 'uuid'")
	}

	ctx := &downloadContext{
		usrCfg:         usrCfg,
		downloadParams: params,
	}
	return ctx, ctx.requestPayload()
}

// requestPayload makes an HTTP request to populate the context payload.
// This is the required entry point for working with downloadContext.
func (d *downloadContext) requestPayload() error {
	client, err := api.NewClient(d.usrCfg.GetString("token"), d.usrCfg.GetString("apibaseurl"))
	if err != nil {
		return err
	}

	req, err := client.NewRequest("GET", d.requestURL(), nil)
	if err != nil {
		return err
	}

	if err = d.buildQuery(req.URL); err != nil {
		return err
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if err := json.NewDecoder(res.Body).Decode(&d.payload); err != nil {
		return fmt.Errorf("unable to parse API response - %s", err)
	}

	if res.StatusCode == http.StatusUnauthorized {
		siteURL := config.InferSiteURL(d.usrCfg.GetString("apibaseurl"))
		return fmt.Errorf("unauthorized request. Please run the configure command. You can find your API token at %s/my/settings", siteURL)
	}
	if res.StatusCode != http.StatusOK {
		switch d.payload.Error.Type {
		case "track_ambiguous":
			return fmt.Errorf("%s: %s", d.payload.Error.Message, strings.Join(d.payload.Error.PossibleTrackIDs, ", "))
		default:
			return errors.New(d.payload.Error.Message)
		}
	}

	return nil
}

// writeSolutionFiles attempts to write each solution file in the payload.
// An HTTP request is made for each file and failed responses are swallowed.
// All successful file responses are written except where empty.
func (d *downloadContext) writeSolutionFiles(exercise workspace.Exercise) error {
	if err := d.validatePayload(); err != nil {
		return err
	}

	for _, filename := range d.payload.Solution.Files {
		res, err := d.requestFile(filename)
		if err != nil {
			return err
		}
		if res == nil {
			continue
		}
		defer res.Body.Close()

		// TODO: if there's a collision, interactively resolve (show diff, ask if overwrite).
		// TODO: handle --force flag to overwrite without asking.

		relativePath := d.sanitizeLegacyFilepath(filename, exercise.Slug)
		dir := filepath.Join(exercise.MetadataDir(), filepath.Dir(relativePath))
		if err = os.MkdirAll(dir, os.FileMode(0755)); err != nil {
			return err
		}

		f, err := os.Create(filepath.Join(exercise.MetadataDir(), relativePath))
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := io.Copy(f, res.Body); err != nil {
			return err
		}
	}
	return nil
}

func (d *downloadContext) requestFile(filename string) (*http.Response, error) {
	if err := d.validatePayload(); err != nil {
		return nil, err
	}
	if filename == "" {
		return nil, errors.New("filename is empty")
	}

	unparsedURL := fmt.Sprintf("%s%s", d.payload.Solution.FileDownloadBaseURL, filename)
	parsedURL, err := netURL.ParseRequestURI(unparsedURL)
	if err != nil {
		return nil, err
	}

	client, err := api.NewClient(d.usrCfg.GetString("token"), d.usrCfg.GetString("apibaseurl"))
	req, err := client.NewRequest("GET", parsedURL.String(), nil)
	if err != nil {
		return nil, err
	}

	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	if res.StatusCode != http.StatusOK {
		// TODO: deal with it
		return nil, nil
	}
	// Don't bother with empty files.
	if res.Header.Get("Content-Length") == "0" {
		return nil, nil
	}

	return res, nil
}

func (d *downloadContext) writeMetadata(exercise workspace.Exercise) error {
	metadata, err := d.metadata()
	if err != nil {
		return err
	}

	if err := metadata.Write(exercise.MetadataDir()); err != nil {
		return err
	}
	return nil
}

func (d *downloadContext) exercise() (workspace.Exercise, error) {
	if err := d.validatePayload(); err != nil {
		return workspace.Exercise{}, err
	}

	root := d.usrCfg.GetString("workspace")
	if d.payload.Solution.Team.Slug != "" {
		root = filepath.Join(root, "teams", d.payload.Solution.Team.Slug)
	}
	if !d.payload.Solution.User.IsRequester {
		root = filepath.Join(root, "users", d.payload.Solution.User.Handle)
	}
	return workspace.Exercise{
		Root:  root,
		Track: d.payload.Solution.Exercise.Track.ID,
		Slug:  d.payload.Solution.Exercise.ID,
	}, nil
}

func (d *downloadContext) metadata() (workspace.ExerciseMetadata, error) {
	if err := d.validatePayload(); err != nil {
		return workspace.ExerciseMetadata{}, err
	}

	return workspace.ExerciseMetadata{
		AutoApprove: d.payload.Solution.Exercise.AutoApprove,
		Track:       d.payload.Solution.Exercise.Track.ID,
		Team:        d.payload.Solution.Team.Slug,
		Exercise:    d.payload.Solution.Exercise.ID,
		ID:          d.payload.Solution.ID,
		URL:         d.payload.Solution.URL,
		Handle:      d.payload.Solution.User.Handle,
		IsRequester: d.payload.Solution.User.IsRequester,
	}, nil
}

func (d *downloadContext) validatePayload() error {
	if d.payload == nil {
		return errors.New("download payload is empty")
	}
	if d.payload.Error.Message != "" {
		return errors.New(d.payload.Error.Message)
	}
	return nil
}

func (d *downloadContext) requestURL() string {
	id := "latest"
	if d.uuid != "" {
		id = d.uuid
	}
	return fmt.Sprintf("%s/solutions/%s", d.usrCfg.GetString("apibaseurl"), id)
}

func (d *downloadContext) buildQuery(url *netURL.URL) error {
	if url == nil {
		return errors.New("url is empty")
	}

	query := url.Query()
	if d.uuid == "" {
		query.Add("exercise_id", d.slug)
		if d.track != "" {
			query.Add("track_id", d.track)
		}
		if d.team != "" {
			query.Add("team_id", d.team)
		}
	}
	url.RawQuery = query.Encode()

	return nil
}

// sanitizeLegacyFilepath is a workaround for a path bug due to an early design
// decision (later reversed) to allow numeric suffixes for exercise directories,
// allowing people to have multiple parallel versions of an exercise.
func (d *downloadContext) sanitizeLegacyFilepath(file, slug string) string {
	pattern := fmt.Sprintf(`\A.*[/\\]%s-\d*/`, slug)
	rgxNumericSuffix := regexp.MustCompile(pattern)
	if rgxNumericSuffix.MatchString(file) {
		file = string(rgxNumericSuffix.ReplaceAll([]byte(file), []byte("")))
	}

	// Rewrite paths submitted with an older, buggy client where the Windows
	// path is being treated as part of the filename.
	file = strings.Replace(file, "\\", "/", -1)

	return filepath.FromSlash(file)
}

// downloadParams is required to create a downloadContext.
type downloadParams struct {
	uuid  string
	slug  string
	track string
	team  string
}

func newDownloadParamsFromExercise(exercise workspace.Exercise) (*downloadParams, error) {
	if exercise.Slug == "" {
		return nil, errors.New("exercise slug is empty")
	}
	return &downloadParams{slug: exercise.Slug, track: exercise.Track}, nil
}

func newDownloadParamsFromFlags(flags *pflag.FlagSet) (*downloadParams, error) {
	var err error
	d := &downloadParams{}

	d.uuid, err = flags.GetString("uuid")
	if err != nil {
		return nil, err
	}
	d.slug, err = flags.GetString("exercise")
	if err != nil {
		return nil, err
	}

	if err = d.validate(); err != nil {
		return nil, errors.New("need an --exercise name or a solution --uuid")
	}

	d.track, err = flags.GetString("track")
	if err != nil {
		return nil, err
	}
	d.team, err = flags.GetString("team")
	if err != nil {
		return nil, err
	}
	return d, err
}

func (d *downloadParams) validate() error {
	if d.slug != "" && d.uuid != "" || d.uuid == d.slug {
		return errors.New("need a 'slug' or a 'uuid'")
	}
	return nil
}

type downloadPayload struct {
	Solution struct {
		ID   string `json:"id"`
		URL  string `json:"url"`
		Team struct {
			Name string `json:"name"`
			Slug string `json:"slug"`
		} `json:"team"`
		User struct {
			Handle      string `json:"handle"`
			IsRequester bool   `json:"is_requester"`
		} `json:"user"`
		Exercise struct {
			ID              string `json:"id"`
			InstructionsURL string `json:"instructions_url"`
			AutoApprove     bool   `json:"auto_approve"`
			Track           struct {
				ID       string `json:"id"`
				Language string `json:"language"`
			} `json:"track"`
		} `json:"exercise"`
		FileDownloadBaseURL string   `json:"file_download_base_url"`
		Files               []string `json:"files"`
		Iteration           struct {
			SubmittedAt *string `json:"submitted_at"`
		}
	} `json:"solution"`
	Error struct {
		Type             string   `json:"type"`
		Message          string   `json:"message"`
		PossibleTrackIDs []string `json:"possible_track_ids"`
	} `json:"error,omitempty"`
}
