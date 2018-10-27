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
)

type downloadParams struct {
	cfg   config.Config
	uuid  string
	slug  string
	track string
	team  string
}

func newDownloadPayload(params downloadParams) (*downloadPayload, error) {
	usrCfg := params.cfg.UserViperConfig

	solutionURL := "latest"
	if params.uuid != "" {
		solutionURL = params.uuid
	}

	url := fmt.Sprintf("%s/solutions/%s", usrCfg.GetString("apibaseurl"), solutionURL)

	client, err := api.NewClient(usrCfg.GetString("token"), usrCfg.GetString("apibaseurl"))
	if err != nil {
		return nil, err
	}

	req, err := client.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	if params.uuid == "" {
		q := req.URL.Query()
		q.Add("exercise_id", params.slug)
		if params.track != "" {
			q.Add("track_id", params.track)
		}
		if params.team != "" {
			q.Add("team_id", params.team)
		}
		req.URL.RawQuery = q.Encode()
	}

	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	var payload *downloadPayload
	defer res.Body.Close()
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("unable to parse API response - %s", err)
	}

	if res.StatusCode == http.StatusUnauthorized {
		siteURL := config.InferSiteURL(usrCfg.GetString("apibaseurl"))
		return nil, fmt.Errorf("unauthorized request. Please run the configure command. You can find your API token at %s/my/settings", siteURL)
	}

	if res.StatusCode != http.StatusOK {
		switch payload.Error.Type {
		case "track_ambiguous":
			return nil, fmt.Errorf("%s: %s", payload.Error.Message, strings.Join(payload.Error.PossibleTrackIDs, ", "))
		default:
			return nil, errors.New(payload.Error.Message)
		}
	}

	return payload, nil
}

func (payload *downloadPayload) writeMetadata(cfg config.Config) error {
	if payload.Error.Message != "" {
		return errors.New(payload.Error.Message)
	}

	metadata := payload.getMetadata()
	exercise := getExerciseFromMetadata(metadata, cfg)

	if err := metadata.Write(exercise.MetadataDir()); err != nil {
		return err
	}

	return nil
}

func (payload *downloadPayload) writeSolutionFiles(cfg config.Config) error {
	if payload.Error.Message != "" {
		return errors.New(payload.Error.Message)
	}
	usrCfg := cfg.UserViperConfig

	exercise := payload.getExercise(cfg)

	for _, file := range payload.Solution.Files {
		unparsedURL := fmt.Sprintf("%s%s", payload.Solution.FileDownloadBaseURL, file)
		parsedURL, err := netURL.ParseRequestURI(unparsedURL)
		if err != nil {
			return err
		}

		client, err := api.NewClient(usrCfg.GetString("token"), usrCfg.GetString("apibaseurl"))
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
		pattern := fmt.Sprintf(`\A.*[/\\]%s-\d*/`, payload.Solution.Exercise.ID)
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

func (payload *downloadPayload) getMetadata() workspace.ExerciseMetadata {
	return workspace.ExerciseMetadata{
		AutoApprove: payload.Solution.Exercise.AutoApprove,
		Track:       payload.Solution.Exercise.Track.ID,
		Team:        payload.Solution.Team.Slug,
		Exercise:    payload.Solution.Exercise.ID,
		ID:          payload.Solution.ID,
		URL:         payload.Solution.URL,
		Handle:      payload.Solution.User.Handle,
		IsRequester: payload.Solution.User.IsRequester,
	}
}

func (payload *downloadPayload) getExercise(cfg config.Config) workspace.Exercise {
	metadata := payload.getMetadata()
	return getExerciseFromMetadata(metadata, cfg)
}

func getExerciseFromMetadata(metadata workspace.ExerciseMetadata, cfg config.Config) workspace.Exercise {
	usrCfg := cfg.UserViperConfig

	root := usrCfg.GetString("workspace")
	if metadata.Team != "" {
		root = filepath.Join(root, "teams", metadata.Team)
	}
	if !metadata.IsRequester {
		root = filepath.Join(root, "users", metadata.Handle)
	}

	return workspace.Exercise{
		Root:  root,
		Track: metadata.Track,
		Slug:  metadata.Exercise,
	}
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