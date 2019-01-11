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
	ws "github.com/exercism/cli/workspace"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// Download is a download from the Exercism API.
type Download struct {
	*downloadParams
	*downloadPayload
	*downloadWriter
}

// NewDownloadFromFlags initiates a Download from flags.
// This is the primary interaction for downloading from the Exercism API.
func NewDownloadFromFlags(usrCfg *viper.Viper, flags *pflag.FlagSet) (*Download, error) {
	downloadParams, err := newDownloadParamsFromFlags(usrCfg, flags)
	if err != nil {
		return nil, err
	}
	return newDownload(downloadParams)
}

// NewDownloadFromExercise initiates a Download from an exercise.
// This is used to get metadata and isn't the primary interaction for downloading.
// Only allows writing metadata, not exercise files.
func NewDownloadFromExercise(usrCfg *viper.Viper, exercise ws.Exercise) (*Download, error) {
	downloadParams, err := newDownloadParamsFromExercise(usrCfg, exercise)
	if err != nil {
		return nil, err
	}
	return newDownload(downloadParams)
}

// newDownload initiates a Download by requesting a downloadPayload from the Exercism API.
func newDownload(params *downloadParams) (*Download, error) {
	if err := params.validate(); err != nil {
		return nil, err
	}
	d := &Download{downloadParams: params}

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
	d.downloadWriter = newDownloadWriter(d)
	return d, d.validate()
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
// Non-200 responses and 0 Content-Length responses are swallowed, returning nil.
func (d *Download) requestFile(filename string) (*http.Response, error) {
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

// exercise creates an exercise, setting its root based on the solution
// being owned by a team or another user.
func (d *Download) exercise() ws.Exercise {
	root := d.usrCfg.GetString("workspace")
	if d.Solution.Team.Slug != "" {
		root = filepath.Join(root, "teams", d.Solution.Team.Slug)
	}
	if !d.Solution.User.IsRequester {
		root = filepath.Join(root, "users", d.Solution.User.Handle)
	}
	return ws.Exercise{
		Root:  root,
		Track: d.Solution.Exercise.Track.ID,
		Slug:  d.Solution.Exercise.ID,
	}
}

func (d *Download) metadata() ws.ExerciseMetadata {
	return ws.ExerciseMetadata{
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

// validate validates the presence of an ID and checks for any error responses.
func (d *Download) validate() error {
	if d.Solution.ID == "" {
		return errors.New("Download missing ID")
	}
	if d.Error.Message != "" {
		return errors.New(d.Error.Message)
	}
	return nil
}

type fileWriter interface {
	Write(f string) error
}

type fileRequester interface {
	requestFile(f string) (*http.Response, error)
}

// downloadWriter writes contents from Download.
type downloadWriter struct {
	download       *Download
	metadataWriter fileWriter
	requester      fileRequester
}

func newDownloadWriter(d *Download) *downloadWriter {
	metadata := d.metadata()
	return &downloadWriter{
		download:       d,
		metadataWriter: &metadata,
		requester:      d,
	}
}

// WriteMetadata writes the exercise metadata.
func (d downloadWriter) WriteMetadata() error {
	return d.metadataWriter.Write(d.Destination())
}

// WriteSolutionFiles attempts to write each exercise file that is part of the downloaded Solution.
// An HTTP request is made using each filename and failed responses are swallowed.
// All successful file responses are written except when 0 Content-Length.
func (d downloadWriter) WriteSolutionFiles() error {
	if d.download.fromExercise {
		return errors.New("existing exercise files should not be overwritten")
	}
	for _, filename := range d.download.Solution.Files {
		res, err := d.requester.requestFile(filename)
		if err != nil {
			return err
		}
		if res == nil {
			continue
		}
		defer res.Body.Close()

		// TODO: if there's a collision, interactively resolve (show diff, ask if overwrite).
		// TODO: handle --force flag to overwrite without asking.

		sanitizedPath := sanitizeLegacyNumericSuffixFilepath(filename, d.download.exercise().Slug)
		fileWritePath := filepath.Join(d.Destination(), sanitizedPath)
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

// Destination is the download destination path.
func (d *downloadWriter) Destination() string {
	return d.download.exercise().MetadataDir()
}

// downloadParams is required to create a Download.
type downloadParams struct {
	usrCfg *viper.Viper
	uuid   string
	slug   string
	track  string
	team   string

	fromFlags    bool
	fromExercise bool
}

func newDownloadParamsFromFlags(usrCfg *viper.Viper, flags *pflag.FlagSet) (*downloadParams, error) {
	d := &downloadParams{usrCfg: usrCfg, fromFlags: true}
	var err error
	d.uuid, err = flags.GetString("uuid")
	if err != nil {
		return nil, err
	}
	d.slug, err = flags.GetString("exercise")
	if err != nil {
		return nil, err
	}
	d.track, err = flags.GetString("track")
	if err != nil {
		return nil, err
	}
	d.team, err = flags.GetString("team")
	if err != nil {
		return nil, err
	}
	return d, d.validate()
}

func newDownloadParamsFromExercise(usrCfg *viper.Viper, exercise ws.Exercise) (*downloadParams, error) {
	d := &downloadParams{
		usrCfg:       usrCfg,
		slug:         exercise.Slug,
		track:        exercise.Track,
		fromExercise: true,
	}
	return d, d.validate()
}

// validate validates the presence of required downloadParams.
func (d *downloadParams) validate() error {
	if d.slug != "" && d.uuid != "" || d.uuid == d.slug {
		if d.fromFlags {
			return errors.New("need an --exercise name or a solution --uuid")
		}
		return errors.New("need a 'slug' or a 'uuid'")
	}

	requiredUsrCfgs := [...]string{
		"token",
		"workspace",
		"apibaseurl",
	}
	for _, cfg := range requiredUsrCfgs {
		if d.usrCfg.GetString(cfg) == "" {
			return fmt.Errorf("missing required user config: '%s'", cfg)
		}
	}
	return nil
}

// sanitizeLegacyNumericSuffixFilepath is a workaround for a path bug due to an early design
// decision (later reversed) to allow numeric suffixes for exercise directories,
// allowing people to have multiple parallel versions of an exercise.
func sanitizeLegacyNumericSuffixFilepath(file, slug string) string {
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

// downloadPayload is an Exercism API response.
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
