package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/exercism/cli/workspace"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

const _workspace = "/home/username"
const apibaseurl = "http://example.com"
const handle = "alice"
const team = "bogus-team"
const track = "bogus-track"
const slug = "bogus-exercise"

func newFakeConfig() *viper.Viper {
	v := viper.New()
	v.Set("token", "abc123")
	v.Set("workspace", _workspace)
	v.Set("apibaseurl", apibaseurl)
	return v
}

func newFakeFlags() *pflag.FlagSet {
	flags := pflag.NewFlagSet("fake", pflag.PanicOnError)
	flags.String("uuid", "", "")
	flags.String("exercise", "bogus-exercise", "")
	flags.String("track", "bogus-track", "")
	flags.String("team", "bogus-team", "")
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
		flags := newFakeFlags()

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
		flags := newFakeFlags()

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
		flags := newFakeFlags()

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

func TestExercise(t *testing.T) {
	var tests = []struct {
		name        string
		team        string
		isRequestor bool
		expected    workspace.Exercise
	}{
		{
			"team, is requestor", team, true,
			workspace.Exercise{
				Root:  filepath.Join(_workspace, "teams", team),
				Track: track,
				Slug:  slug,
			},
		},
		{
			"no team, is requestor", "", true,
			workspace.Exercise{
				Root:  filepath.Join(_workspace),
				Track: track,
				Slug:  slug,
			},
		},
		{
			"no team, not requestor", "", false,
			workspace.Exercise{
				Root:  filepath.Join(_workspace, "users", handle),
				Track: track,
				Slug:  slug,
			},
		},
		{
			"team, not requestor", team, false,
			workspace.Exercise{
				Root:  filepath.Join(_workspace, "teams", team, "users", handle),
				Track: track,
				Slug:  slug,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dl, err := newFakeDownload(downloadPayloadTmpl)
			assert.NoError(t, err)

			dl.Solution.Team.Slug = tt.team
			dl.Solution.User.IsRequester = tt.isRequestor
			given := dl.Exercise()

			assert.Equal(t, given, tt.expected)
		})
	}
}

func newFakeDownload(template string) (*Download, error) {
	d := &Download{DownloadParams: newFakeDownloadParams()}
	if err := json.Unmarshal([]byte(template), &d.downloadPayload); err != nil {
		return nil, err
	}
	return d, nil
}

type mockWriter struct{ called bool }

func (m *mockWriter) Write(dir string) error {
	m.called = true
	return nil
}

func TestWriteMetadata(t *testing.T) {
	t.Run("delegates Write", func(t *testing.T) {
		dl, err := newFakeDownload(downloadPayloadTmpl)
		assert.NoError(t, err)

		mock := &mockWriter{}
		writer := &DownloadWriter{Download: dl, fileWriter: mock}

		err = writer.WriteMetadata()
		assert.NoError(t, err)

		if !mock.called {
			t.Error("expected Write to be called")
		}
	})
}

type stubDownloader struct{}

// stubs requestFile, returning a resp body containing the given filename.
func (m stubDownloader) requestFile(filename string) (*http.Response, error) {
	// shouldn't write empty files
	if filename == "empty" {
		return nil, nil
	}
	return &http.Response{
		Body: ioutil.NopCloser(bytes.NewBufferString(filename)),
	}, nil
}

func TestWriteSolutionFiles(t *testing.T) {
	t.Run("requests payload Solution files and saves", func(t *testing.T) {
		dl, err := newFakeDownload(downloadPayloadTmpl)
		assert.NoError(t, err)

		tmpDir, err := ioutil.TempDir("", "download-service")
		defer os.RemoveAll(tmpDir)
		assert.NoError(t, err)
		dl.usrCfg.Set("workspace", tmpDir)

		writer := &DownloadWriter{Download: dl, downloader: &stubDownloader{}}

		err = writer.WriteSolutionFiles()
		assert.NoError(t, err)

		targetDir := filepath.Join(tmpDir, "teams", "bogus-team-slug", "users", handle)
		assertDownloadedCorrectFiles(t, targetDir)
	})
}

func assertDownloadedCorrectFiles(t *testing.T, targetDir string) {
	expectedFiles := []struct {
		desc     string
		path     string
		contents string
	}{
		{
			desc:     "a file in the exercise root directory",
			path:     filepath.Join(targetDir, "bogus-track", "bogus-exercise", "file-1.txt"),
			contents: "file-1.txt",
		},
		{
			desc:     "a file in a subdirectory",
			path:     filepath.Join(targetDir, "bogus-track", "bogus-exercise", "subdir", "file-2.txt"),
			contents: "subdir/file-2.txt",
		},
		{
			desc:     "a path with a numeric suffix",
			path:     filepath.Join(targetDir, "bogus-track", "bogus-exercise", "subdir", "numeric.txt"),
			contents: "/full/path/with/numeric-suffix/bogus-track/bogus-exercise-12345/subdir/numeric.txt",
		},
		{
			desc:     "a file that requires URL encoding",
			path:     filepath.Join(targetDir, "bogus-track", "bogus-exercise", "special-char-filename#.txt"),
			contents: "special-char-filename#.txt",
		},
		{
			desc:     "a file that has a leading slash",
			path:     filepath.Join(targetDir, "bogus-track", "bogus-exercise", "with-leading-slash.txt"),
			contents: "/with-leading-slash.txt",
		},
		{
			desc:     "a file with a leading backslash",
			path:     filepath.Join(targetDir, "bogus-track", "bogus-exercise", "with-leading-backslash.txt"),
			contents: "\\with-leading-backslash.txt",
		},
		{
			desc:     "a file with backslashes in path",
			path:     filepath.Join(targetDir, "bogus-track", "bogus-exercise", "with", "backslashes", "in", "path.txt"),
			contents: "\\with\\backslashes\\in\\path.txt",
		},
	}

	for _, file := range expectedFiles {
		t.Run(file.desc, func(t *testing.T) {
			b, err := ioutil.ReadFile(file.path)
			assert.NoError(t, err)
			assert.Equal(t, file.contents, string(b))
		})
	}

	path := filepath.Join(targetDir, "bogus-track", "bogus-exercise", "empty")
	_, err := os.Lstat(path)
	assert.True(t, os.IsNotExist(err), "It should not write the file if empty.")
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
			"/full/path/with/numeric-suffix/bogus-track/bogus-exercise-12345/subdir/numeric.txt",
			"empty"
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
