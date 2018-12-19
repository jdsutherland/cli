package service

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

type fileWriter interface {
	Write(f string) error
}

type downloader interface {
	requestFile(f string) (*http.Response, error)
}

// DownloadWriter writes metadata and Solution files from a Download to disk.
type DownloadWriter struct {
	*Download
	fileWriter
	downloader
}

// NewDownloadWriter creates a new DownloadWriter from a Download.
func NewDownloadWriter(download *Download) (*DownloadWriter, error) {
	if err := download.validate(); err != nil {
		return nil, err
	}
	return &DownloadWriter{Download: download}, nil
}

// WriteMetadata writes metadata from the download.
func (d *DownloadWriter) WriteMetadata() error {
	if d.fileWriter == nil {
		d.fileWriter = d.metadata()
	}
	return d.fileWriter.Write(d.Exercise().MetadataDir())
}

// WriteSolutionFiles attempts to write each Solution file in the Download.
// An HTTP request is made for each file and failed responses are swallowed.
// All successful file responses are written except where empty.
func (d *DownloadWriter) WriteSolutionFiles() error {
	if d.downloader == nil {
		d.downloader = d.Download
	}
	for _, filename := range d.Solution.Files {
		res, err := d.downloader.requestFile(filename)
		if err != nil {
			return err
		}
		if res == nil {
			continue
		}
		defer res.Body.Close()

		// TODO: if there's a collision, interactively resolve (show diff, ask if overwrite).
		// TODO: handle --force flag to overwrite without asking.

		sanitizedPath := d.sanitizeLegacyFilepath(filename, d.Exercise().Slug)
		fileWritePath := filepath.Join(d.Exercise().MetadataDir(), sanitizedPath)
		if err = os.MkdirAll(filepath.Dir(fileWritePath), os.FileMode(0755)); err != nil {
			return err
		}

		f, err := os.Create(fileWritePath)
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

// sanitizeLegacyFilepath is a workaround for a path bug due to an early design
// decision (later reversed) to allow numeric suffixes for exercise directories,
// allowing people to have multiple parallel versions of an exercise.
func (d DownloadWriter) sanitizeLegacyFilepath(file, slug string) string {
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

// DownloadParams is required to create a Download.
type DownloadParams struct {
	usrCfg *viper.Viper
	uuid   string
	slug   string
	track  string
	team   string
}

// NewDownloadParamsFromExercise creates a new DownloadParams from an Exercise.
func NewDownloadParamsFromExercise(usrCfg *viper.Viper, exercise workspace.Exercise) (*DownloadParams, error) {
	d := &DownloadParams{usrCfg: usrCfg, slug: exercise.Slug, track: exercise.Track}
	return d, d.validate()
}

// NewDownloadParamsFromFlags creates a new DownloadParams from flags.
func NewDownloadParamsFromFlags(usrCfg *viper.Viper, flags *pflag.FlagSet) (*DownloadParams, error) {
	if flags == nil {
		return nil, errors.New("flags is empty")
	}

	var err error
	d := &DownloadParams{usrCfg: usrCfg}

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

func (d *DownloadParams) validate() error {
	if d == nil {
		return errors.New("DownloadParams is empty")
	}
	if d.slug != "" && d.uuid != "" || d.uuid == d.slug {
		return errors.New("need a 'slug' or a 'uuid'")
	}
	if d.usrCfg == nil {
		return errors.New("user config is empty")
	}
	requiredCfgs := [...]string{
		"token",
		"workspace",
		"apibaseurl",
	}
	for _, cfg := range requiredCfgs {
		if d.usrCfg.GetString(cfg) == "" {
			return fmt.Errorf("missing required UserViperConfig '%s'", cfg)
		}
	}
	return nil
}

// Download represents a Download from the Exercism API.
type Download struct {
	*DownloadParams
	*downloadPayload
}

// NewDownload creates a Download, getting a downloadPayload from the Exercism API.
func NewDownload(params *DownloadParams) (*Download, error) {
	if err := params.validate(); err != nil {
		return nil, err
	}
	d := &Download{DownloadParams: params}

	client, err := api.NewClient(d.usrCfg.GetString("token"), d.usrCfg.GetString("apibaseurl"))
	if err != nil {
		return nil, err
	}

	req, err := client.NewRequest("GET", d.requestURL(), nil)
	if err != nil {
		return nil, err
	}
	d.buildQuery(req.URL)
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if err := json.NewDecoder(res.Body).Decode(&d.downloadPayload); err != nil {
		return nil, fmt.Errorf("unable to parse API response - %s", err)
	}

	if res.StatusCode == http.StatusUnauthorized {
		siteURL := config.InferSiteURL(d.usrCfg.GetString("apibaseurl"))
		return nil, fmt.Errorf("unauthorized request. Please run the configure command. You can find your API token at %s/my/settings", siteURL)
	}
	if res.StatusCode != http.StatusOK {
		switch d.Error.Type {
		case "track_ambiguous":
			return nil, fmt.Errorf("%s: %s", d.Error.Message, strings.Join(d.Error.PossibleTrackIDs, ", "))
		default:
			return nil, errors.New(d.Error.Message)
		}
	}
	return d, nil
}

func (d *Download) requestURL() string {
	id := "latest"
	if d.uuid != "" {
		id = d.uuid
	}
	return fmt.Sprintf("%s/solutions/%s", d.usrCfg.GetString("apibaseurl"), id)
}

func (d *Download) buildQuery(url *netURL.URL) {
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
}

// requestFile requests a Solution file from the API, returning an HTTP response.
// Non 200 responses and zero length file responses are swallowed, returning nil.
func (d *Download) requestFile(filename string) (*http.Response, error) {
	if filename == "" {
		return nil, errors.New("filename is empty")
	}

	unparsedURL := fmt.Sprintf("%s%s", d.Solution.FileDownloadBaseURL, filename)
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

// Exercise creates an Exercise from the payload.
func (d *Download) Exercise() workspace.Exercise {
	root := d.usrCfg.GetString("workspace")
	if d.Solution.Team.Slug != "" {
		root = filepath.Join(root, "teams", d.Solution.Team.Slug)
	}
	if !d.Solution.User.IsRequester {
		root = filepath.Join(root, "users", d.Solution.User.Handle)
	}
	return workspace.Exercise{
		Root:  root,
		Track: d.Solution.Exercise.Track.ID,
		Slug:  d.Solution.Exercise.ID,
	}
}

func (d *Download) metadata() *workspace.ExerciseMetadata {
	return &workspace.ExerciseMetadata{
		AutoApprove: d.Solution.Exercise.AutoApprove,
		Track:       d.Solution.Exercise.Track.ID,
		Team:        d.Solution.Team.Slug,
		Exercise:    d.Solution.Exercise.ID,
		ID:          d.Solution.ID,
		URL:         d.Solution.URL,
		Handle:      d.Solution.User.Handle,
		IsRequester: d.Solution.User.IsRequester,
	}
}

func (d *Download) validate() error {
	if d == nil || d.Solution.ID == "" {
		return errors.New("Download is empty")
	}
	if d.Error.Message != "" {
		return errors.New(d.Error.Message)
	}
	return nil
}

// downloadPayload is an Exercism API response.
type downloadPayload struct {
	*DownloadParams
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
