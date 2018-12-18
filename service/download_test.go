package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/exercism/cli/workspace"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

const _workspace = "/home/username"
const apibaseurl = "http://example.com"

func newFakeConfig() *viper.Viper {
	v := viper.New()
	v.Set("token", "abc123")
	v.Set("workspace", _workspace)
	v.Set("apibaseurl", apibaseurl)
	return v
}

type paramFlags struct {
	uuid     string
	exercise string
	track    string
	team     string
}

func (d *paramFlags) populateValid() {
	d.exercise = "bogus-exercise"
	d.track = "bogus-track"
	d.team = "bogus-team"
}

func newParamFlags() paramFlags {
	params := paramFlags{}
	params.populateValid()
	return params
}

func newFakeFlags(params paramFlags) *pflag.FlagSet {
	flags := pflag.NewFlagSet("fake", pflag.PanicOnError)
	flags.String("uuid", params.uuid, "")
	flags.String("track", params.track, "")
	flags.String("exercise", params.exercise, "")
	flags.String("team", params.team, "")
	return flags
}

func newFakeDownloadParams() *DownloadParams {
	cfg := newFakeConfig()
	return &DownloadParams{
		usrCfg: cfg,
		slug:   "bogus-exercise",
		track:  "bogus-track",
		team:   "bogus-team",
	}
}

func TestNewDownloadParamFromExercise(t *testing.T) {
	t.Run("creates succesfully when valid", func(t *testing.T) {
		cfg := newFakeConfig()
		exercise := workspace.Exercise{
			Slug:  "bogus-exercise",
			Track: "bogus-track",
		}

		got, err := NewDownloadParamsFromExercise(cfg, exercise)
		assert.NoError(t, err)

		want := &DownloadParams{
			usrCfg: cfg,
			slug:   "bogus-exercise",
			track:  "bogus-track",
		}

		assert.Equal(t, got.usrCfg, want.usrCfg)
		assert.Equal(t, got.slug, want.slug)
		assert.Equal(t, got.track, want.track)
	})

	t.Run("validates exercise", func(t *testing.T) {
		cfg := newFakeConfig()
		exercise := workspace.Exercise{
			Slug:  "bogus-exercise",
			Track: "bogus-track",
		}

		_, err := NewDownloadParamsFromExercise(cfg, exercise)
		assert.NoError(t, err)

		exercise.Slug = ""
		_, err = NewDownloadParamsFromExercise(cfg, exercise)
		assert.Error(t, err)
	})

	t.Run("validates user config", func(t *testing.T) {
		cfg := newFakeConfig()
		exercise := workspace.Exercise{
			Slug:  "bogus-exercise",
			Track: "bogus-track",
		}

		_, err := NewDownloadParamsFromExercise(nil, exercise)
		assert.Error(t, err)

		_, err = NewDownloadParamsFromExercise(cfg, exercise)
		assert.NoError(t, err)

		cfg.Set("token", "")
		_, err = NewDownloadParamsFromExercise(cfg, exercise)
		assert.Error(t, err)

		cfg = newFakeConfig()
		cfg.Set("workspace", "")
		_, err = NewDownloadParamsFromExercise(cfg, exercise)
		assert.Error(t, err)

		cfg = newFakeConfig()
		cfg.Set("apibaseurl", "")
		_, err = NewDownloadParamsFromExercise(cfg, exercise)
		assert.Error(t, err)
	})
}

func TestNewDownloadParamFromFlags(t *testing.T) {
	t.Run("creates successfully when valid", func(t *testing.T) {
		cfg := newFakeConfig()
		flags := newFakeFlags(newParamFlags())

		got, err := NewDownloadParamsFromFlags(cfg, flags)
		assert.NoError(t, err)

		want := &DownloadParams{
			usrCfg: cfg,
			slug:   "bogus-exercise",
			track:  "bogus-track",
			team:   "bogus-team",
		}

		assert.Equal(t, got.usrCfg, want.usrCfg)
		assert.Equal(t, got.slug, want.slug)
		assert.Equal(t, got.track, want.track)
		assert.Equal(t, got.team, want.team)
	})

	t.Run("validates flags", func(t *testing.T) {
		cfg := newFakeConfig()
		flags := newFakeFlags(newParamFlags())

		_, err := NewDownloadParamsFromFlags(cfg, nil)
		assert.Error(t, err)

		_, err = NewDownloadParamsFromFlags(cfg, flags)
		assert.NoError(t, err)

		// requires either exercise or uuid
		flags.Set("exercise", "")
		_, err = NewDownloadParamsFromFlags(cfg, flags)
		assert.Error(t, err)

		flags.Set("uuid", "bogus-uuid")
		_, err = NewDownloadParamsFromFlags(cfg, flags)
		assert.NoError(t, err)
	})

	t.Run("validates user config", func(t *testing.T) {
		var err error
		cfg := newFakeConfig()
		flags := newFakeFlags(newParamFlags())

		_, err = NewDownloadParamsFromFlags(nil, flags)
		assert.Error(t, err)

		_, err = NewDownloadParamsFromFlags(cfg, flags)
		assert.NoError(t, err)

		cfg.Set("token", "")
		_, err = NewDownloadParamsFromFlags(cfg, flags)
		assert.Error(t, err)

		cfg = newFakeConfig()
		cfg.Set("workspace", "")
		_, err = NewDownloadParamsFromFlags(cfg, flags)
		assert.Error(t, err)

		cfg = newFakeConfig()
		cfg.Set("apibaseurl", "")
		_, err = NewDownloadParamsFromFlags(cfg, flags)
		assert.Error(t, err)
	})
}

func fakeDownloadServer() *httptest.Server {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)

	mux.HandleFunc("/valid/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, downloadPayloadTmpl)
	})

	mux.HandleFunc("/unauth/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, errorTmpl)
	})

	mux.HandleFunc("/non200/", func(w http.ResponseWriter, r *http.Request) {
		// use 400 to fulfill a non 200 response
		w.WriteHeader(400)
		fmt.Fprintf(w, errorTmpl)
	})

	mux.HandleFunc("/errors/", func(w http.ResponseWriter, r *http.Request) {
		// use 400 to fulfill a non 200 response
		fmt.Fprintf(w, errorTmpl)
	})

	return server
}

func setFakeServerRoute(usrCfg *viper.Viper, tsURL, route string) {
	testServerURL := fmt.Sprintf("%s/%s", tsURL, route)
	usrCfg.Set("apibaseurl", testServerURL)
}

func TestDownload(t *testing.T) {
	t.Run("creates successfully when valid", func(t *testing.T) {
		ts := fakeDownloadServer()
		defer ts.Close()

		params := newFakeDownloadParams()
		setFakeServerRoute(params.usrCfg, ts.URL, "valid")

		_, err := NewDownload(params)
		assert.NoError(t, err)
	})

	t.Run("401 response", func(t *testing.T) {
		ts := fakeDownloadServer()
		defer ts.Close()

		params := newFakeDownloadParams()
		setFakeServerRoute(params.usrCfg, ts.URL, "unauth")

		_, err := NewDownload(params)
		assert.Error(t, err)
	})

	t.Run("non 200 response", func(t *testing.T) {
		ts := fakeDownloadServer()
		defer ts.Close()

		params := newFakeDownloadParams()
		setFakeServerRoute(params.usrCfg, ts.URL, "non200")

		_, err := NewDownload(params)

		if assert.Error(t, err) {
			assert.Equal(t, err.Error(), "error-msg")
		}
	})

	t.Run("validates", func(t *testing.T) {
		ts := fakeDownloadServer()
		defer ts.Close()

		params := newFakeDownloadParams()
		setFakeServerRoute(params.usrCfg, ts.URL, "errors")

		dl, err := NewDownload(params)
		assert.NoError(t, err)

		err = dl.validate()
		if assert.Error(t, err) {
			assert.Equal(t, err.Error(), "Download is empty")
		}

		dl.Solution.ID = "1"
		err = dl.validate()
		if assert.Error(t, err) {
			assert.Equal(t, err.Error(), "error-msg")
		}
	})
}

func TestDownload_Exercise(t *testing.T) {
	const handle = "alice"
	const team = "bogus-team"

	t.Run("team, is requestor", func(t *testing.T) {
		dl, err := newFakeDownload(downloadPayloadTmpl)
		assert.NoError(t, err)

		dl.Solution.Team.Slug = team
		dl.Solution.User.IsRequester = true
		got := dl.Exercise()

		want := workspace.Exercise{
			Root:  filepath.Join(_workspace, "teams", team),
			Track: "bogus-track",
			Slug:  "bogus-exercise",
		}

		if got != want {
			t.Errorf("got '%s', want '%s'", got, want)
		}
	})

	t.Run("no team, is requestor", func(t *testing.T) {
		dl, err := newFakeDownload(downloadPayloadTmpl)
		assert.NoError(t, err)

		dl.Solution.Team.Slug = ""
		dl.Solution.User.IsRequester = true
		got := dl.Exercise()

		want := workspace.Exercise{
			Root:  _workspace,
			Track: "bogus-track",
			Slug:  "bogus-exercise",
		}

		if got != want {
			t.Errorf("got '%s', want '%s'", got, want)
		}
	})

	t.Run("no team, not requestor", func(t *testing.T) {
		dl, err := newFakeDownload(downloadPayloadTmpl)
		assert.NoError(t, err)

		dl.Solution.Team.Slug = ""
		dl.Solution.User.IsRequester = false
		got := dl.Exercise()

		want := workspace.Exercise{
			Root:  filepath.Join(_workspace, "users", handle),
			Track: "bogus-track",
			Slug:  "bogus-exercise",
		}

		if got != want {
			t.Errorf("got '%s', want '%s'", got, want)
		}
	})

	t.Run("team, not requestor", func(t *testing.T) {
		dl, err := newFakeDownload(downloadPayloadTmpl)
		assert.NoError(t, err)

		dl.Solution.Team.Slug = team
		dl.Solution.User.IsRequester = false
		got := dl.Exercise()

		want := workspace.Exercise{
			Root:  filepath.Join(_workspace, "teams", team, "users", handle),
			Track: "bogus-track",
			Slug:  "bogus-exercise",
		}

		if got != want {
			t.Errorf("got '%s', want '%s'", got, want)
		}
	})
}

func newFakeDownload(template string) (*Download, error) {
	d := &Download{DownloadParams: newFakeDownloadParams()}
	if err := json.Unmarshal([]byte(template), &d.downloadPayload); err != nil {
		return nil, err
	}
	return d, nil
}

const downloadPayloadTmpl = `
{
	"solution": {
		"id": "bogus-id",
		"user": {
			"handle": "alice"
		},
		"team": {
			"name": "bogus-team",
			"slug": "bogus-team-slug"
		},
		"exercise": {
			"id": "bogus-exercise",
			"instructions_url": "http://example.com/bogus-exercise",
			"auto_approve": false,
			"track": {
				"id": "bogus-track",
				"language": "Bogus Language"
			}
		},
		"file_download_base_url": "bogus-base-url",
		"files": [
			"file-1.txt",
			"subdir/file-2.txt",
			"special-char-filename#.txt",
			"/with-leading-slash.txt",
			"\\with-leading-backslash.txt",
			"\\with\\backslashes\\in\\path.txt",
			"file-3.txt",
			"/full/path/with/numeric-suffix/bogus-track/bogus-exercise-12345/subdir/numeric.txt"
		],
		"iteration": {
			"submitted_at": "2017-08-21t10:11:12.130z"
		}
	}
}
`

const errorTmpl = `
{
  "error": {
	"type": "bogus",
	"message": "error-msg",
	"possible_track_ids": []
  }
}
`
