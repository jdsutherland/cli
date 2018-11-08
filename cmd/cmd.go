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

type downloadContext struct {
	usrCfg  *viper.Viper
	uuid    string
	slug    string
	track   string
	team    string
	payload *downloadPayload
}

func download(d *downloadContext) error {
	client, err := api.NewClient(d.usrCfg.GetString("token"), d.usrCfg.GetString("apibaseurl"))
	if err != nil {
		return err
	}

	req, err := client.NewRequest("GET", d.requestURL(), nil)
	if err != nil {
		return err
	}

	req.URL.RawQuery = d.query(req.URL)
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

func (d *downloadContext) writeMetadata() error {
	if err := d.validate(); err != nil {
		return err
	}
	metadata := d.metadata()
	exercise := d.exercise()
	if err := metadata.Write(exercise.MetadataDir()); err != nil {
		return err
	}
	return nil
}

func (d *downloadContext) writeSolutionFiles() error {
	if err := d.validate(); err != nil {
		return err
	}
	exercise := d.exercise()
	for _, file := range d.payload.Solution.Files {
		unparsedURL := fmt.Sprintf("%s%s", d.payload.Solution.FileDownloadBaseURL, file)
		parsedURL, err := netURL.ParseRequestURI(unparsedURL)
		if err != nil {
			return err
		}

		client, err := api.NewClient(d.usrCfg.GetString("token"), d.usrCfg.GetString("apibaseurl"))
		req, err := client.NewRequest("GET", parsedURL.String(), nil)
		if err != nil {
			return err
		}

		res, err := client.Do(req)
		if err != nil {
			return err
		}
		defer res.Body.Close()

		if res.StatusCode != http.StatusOK {
			// TODO: deal with it
			continue
		}
		// Don't bother with empty files.
		if res.Header.Get("Content-Length") == "0" {
			continue
		}

		// TODO: if there's a collision, interactively resolve (show diff, ask if overwrite).
		// TODO: handle --force flag to overwrite without asking.

		// Work around a path bug due to an early design decision (later reversed) to
		// allow numeric suffixes for exercise directories, allowing people to have
		// multiple parallel versions of an exercise.
		pattern := fmt.Sprintf(`\A.*[/\\]%s-\d*/`, exercise.Slug)
		rgxNumericSuffix := regexp.MustCompile(pattern)
		if rgxNumericSuffix.MatchString(file) {
			file = string(rgxNumericSuffix.ReplaceAll([]byte(file), []byte("")))
		}

		// Rewrite paths submitted with an older, buggy client where the Windows path is being treated as part of the filename.
		file = strings.Replace(file, "\\", "/", -1)

		relativePath := filepath.FromSlash(file)

		dir := filepath.Join(exercise.MetadataDir(), filepath.Dir(relativePath))
		if err := os.MkdirAll(dir, os.FileMode(0755)); err != nil {
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

func (d *downloadContext) exercise() workspace.Exercise {
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
	}
}

func (d *downloadContext) metadata() workspace.ExerciseMetadata {
	return workspace.ExerciseMetadata{
		AutoApprove: d.payload.Solution.Exercise.AutoApprove,
		Track:       d.payload.Solution.Exercise.Track.ID,
		Team:        d.payload.Solution.Team.Slug,
		Exercise:    d.payload.Solution.Exercise.ID,
		ID:          d.payload.Solution.ID,
		URL:         d.payload.Solution.URL,
		Handle:      d.payload.Solution.User.Handle,
		IsRequester: d.payload.Solution.User.IsRequester,
	}
}

func (d *downloadContext) validate() error {
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

func (d *downloadContext) query(url *netURL.URL) string {
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
	return query.Encode()
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
