package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/exercism/cli/workspace"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

func newFakeConfig() *viper.Viper {
	v := viper.New()
	v.Set("token", "abc123")
	v.Set("workspace", "/home/username")
	v.Set("apibaseurl", "http://example.com")
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

	mux.HandleFunc("/valid", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, payloadTemplate)
	})

	mux.HandleFunc("/unauth", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	mux.HandleFunc("/errors", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		fmt.Fprintf(w, payloadTemplateErrorMessage)
	})

	return server
}

func TestDownloadMetadata(t *testing.T) {
	t.Run("creates successfully when valid", func(t *testing.T) {
		ts := fakeDownloadServer()
		defer ts.Close()

		params := newFakeDownloadParams()
		params.usrCfg.Set("apibaseurl", fmt.Sprint(ts.URL, "/valid"))
		a := fmt.Sprint(ts.URL, "/valid")
		fmt.Printf("\ta: \n%+v\n", a)
		f := params.usrCfg.GetString("apibaseurl")
		fmt.Printf("\tf: \n%+v\n", f)

		_, err := NewDownload(params)
		assert.NoError(t, err)

	})

	// t.Run("unauth response", func(t *testing.T) {
	// 	ts := fakeDownloadServer()
	// 	defer ts.Close()

	// 	params := newFakeDownloadParams()
	// 	params.usrCfg.Set("apibaseurl", ts.URL)

	// 	_, err := NewDownload(params)
	// 	assert.NoError(t, err)
	// })

	t.Run("validates", func(t *testing.T) {

		ts := fakeDownloadServer()
		defer ts.Close()

		params := newFakeDownloadParams()
		params.usrCfg.Set("apibaseurl", ts.URL)

		_, err := NewDownload(nil)
		assert.Error(t, err)
	})
}

type fakeDownloadPayload struct {
	payload *downloadPayload
}

func (m *fakeDownloadPayload) newPayload(template string) error {
	if err := json.Unmarshal([]byte(template), &m.payload); err != nil {
		return err
	}
	return nil
}

const payloadTemplateErrorMessage = `
{
  "error": {
	"type": "bogus",
	"message": "we are error",
	"possible_track_ids": []
  }
}
`

const payloadTemplateSolutionEmpty = `
{
	"solution": {
		"id": "",
		"user": {
			"handle": "alice",
			"is_requester": %s
		},
		"team": %s,
		"exercise": {
			"id": "bogus-exercise",
			"instructions_url": "http://example.com/bogus-exercise",
			"auto_approve": false,
			"track": {
				"id": "bogus-track",
				"language": "Bogus Language"
			}
		},
		"file_download_base_url": "%s",
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
const payloadTemplate = `
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
		"file_download_base_url": "",
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
