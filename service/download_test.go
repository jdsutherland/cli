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

	ws "github.com/exercism/cli/workspace"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

func TestNewDownloadFromExercise(t *testing.T) {
	f := newFixture()
	usrCfg := f.usrCfg()
	ts := newDownloadServerRoutedTo(t, usrCfg, "OK")
	defer ts.Close()

	got, err := NewDownloadFromExercise(usrCfg, f.exercise())
	assert.NoError(t, err)

	params := f.downloadParams()
	// use cfg wired up for test server
	params.usrCfg = usrCfg
	want, err := newDownload(params)
	assert.NoError(t, err)

	assert.Equal(t, got.slug, want.slug)
	assert.Equal(t, got.track, want.track)
	assert.Equal(t, got.Solution.ID, want.Solution.ID)
}

func TestNewDownloadFromFlags(t *testing.T) {
	f := newFixture()
	usrCfg := f.usrCfg()
	ts := newDownloadServerRoutedTo(t, usrCfg, "OK")
	defer ts.Close()

	got, err := NewDownloadFromFlags(usrCfg, f.flags())
	assert.NoError(t, err)

	params := f.downloadParams()
	// use cfg wired up for test server
	params.usrCfg = usrCfg
	want, err := newDownload(params)
	assert.NoError(t, err)

	assert.Equal(t, got.slug, want.slug)
	assert.Equal(t, got.track, want.track)
	assert.Equal(t, got.Solution.ID, want.Solution.ID)
}

type mockWriter struct{ called bool }

func (m *mockWriter) Write(dir string) error {
	m.called = true
	return nil
}

func TestWriteMetadata(t *testing.T) {
	f := newFixture()
	dl, err := f.download()
	assert.NoError(t, err)

	mock := &mockWriter{}
	writer := &downloadWriter{Download: dl, writer: mock}

	err = writer.WriteMetadata()
	assert.NoError(t, err)

	if !mock.called {
		t.Error("expected Write to be called")
	}
}

type stubRequester struct{}

// stubs requestFile, returning a resp body containing the given filename.
func (m stubRequester) requestFile(filename string) (*http.Response, error) {
	// shouldn't write empty files
	if filename == "empty" {
		return nil, nil
	}
	return &http.Response{
		Body: ioutil.NopCloser(bytes.NewBufferString(filename)),
	}, nil
}

func TestWriteSolutionFiles(t *testing.T) {
	t.Run("error when called from download initiated via exercise", func(t *testing.T) {
		f := newFixture()
		params := f.downloadParams()
		params.fromExercise = true

		dl, err := f.download()
		assert.NoError(t, err)
		dl.downloadParams = params

		err = dl.WriteSolutionFiles()
		if assert.Error(t, err) {
			assert.Contains(t, err.Error(), "should not be overwritten")
		}
	})

	t.Run("succesfully writes files", func(t *testing.T) {
		f := newFixture()
		dl, err := f.download()
		assert.NoError(t, err)

		tmpDir, err := ioutil.TempDir("", "download-service")
		defer os.RemoveAll(tmpDir)
		assert.NoError(t, err)
		dl.usrCfg.Set("workspace", tmpDir)

		writer := &downloadWriter{Download: dl, requester: &stubRequester{}}

		err = writer.WriteSolutionFiles()
		assert.NoError(t, err)

		assertDownloadedCorrectFiles(t, f.payloadTmplDir(tmpDir))
	})
}

// paths match files in payloadTmpl.
func assertDownloadedCorrectFiles(t *testing.T, targetDir string) {
	f := newFixture()
	dirname := filepath.Join(targetDir, f.track, f.slug)

	expectedFiles := []struct {
		desc     string
		path     string
		contents string
	}{
		{
			desc:     "a file in the exercise root directory",
			path:     filepath.Join(dirname, "file-1.txt"),
			contents: "file-1.txt",
		},
		{
			desc:     "a file in a subdirectory",
			path:     filepath.Join(dirname, "subdir", "file-2.txt"),
			contents: "subdir/file-2.txt",
		},
		{
			desc:     "a file that requires URL encoding",
			path:     filepath.Join(dirname, "special-char-filename#.txt"),
			contents: "special-char-filename#.txt",
		},
		{
			desc:     "a file that has a leading slash",
			path:     filepath.Join(dirname, "with-leading-slash.txt"),
			contents: "/with-leading-slash.txt",
		},
		{
			desc:     "a file with a leading backslash",
			path:     filepath.Join(dirname, "with-leading-backslash.txt"),
			contents: "\\with-leading-backslash.txt",
		},
		{
			desc:     "a file with backslashes in path",
			path:     filepath.Join(dirname, "with", "backslashes", "in", "path.txt"),
			contents: "\\with\\backslashes\\in\\path.txt",
		},
		{
			desc:     "a path with a numeric suffix",
			path:     filepath.Join(dirname, "subdir", "numeric.txt"),
			contents: "/full/path/with/numeric-suffix/bogus-track/bogus-exercise-12345/subdir/numeric.txt",
		},
	}

	for _, file := range expectedFiles {
		t.Run(file.desc, func(t *testing.T) {
			b, err := ioutil.ReadFile(file.path)
			assert.NoError(t, err)
			assert.Equal(t, file.contents, string(b))
		})
	}

	path := filepath.Join(dirname, "empty")
	_, err := os.Lstat(path)
	assert.True(t, os.IsNotExist(err), "It should not write the file if empty.")
}

func TestDestination(t *testing.T) {
	f := newFixture()
	dl, err := f.download()
	assert.NoError(t, err)

	assert.Equal(t, dl.Destination(), f.exercise().MetadataDir())
}

func TestDownload(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		f := newFixture()
		params := f.downloadParams()
		ts := newDownloadServerRoutedTo(t, params.usrCfg, "OK")
		defer ts.Close()

		_, err := newDownload(params)
		assert.NoError(t, err)
	})

	t.Run("unauthorized", func(t *testing.T) {
		f := newFixture()
		params := f.downloadParams()
		ts := newDownloadServerRoutedTo(t, params.usrCfg, "unauthorized")
		defer ts.Close()

		_, err := newDownload(params)
		if assert.Error(t, err) {
			assert.Contains(t, err.Error(), "unauthorized request")
		}
	})

	t.Run("non 200 response", func(t *testing.T) {
		f := newFixture()
		params := f.downloadParams()
		ts := newDownloadServerRoutedTo(t, params.usrCfg, "non200")
		defer ts.Close()

		_, err := newDownload(params)

		if assert.Error(t, err) {
			assert.Equal(t, err.Error(), "error-msg")
		}
	})

	t.Run("validates after downloading", func(t *testing.T) {
		f := newFixture()
		params := f.downloadParams()
		ts := newDownloadServerRoutedTo(t, params.usrCfg, "errors")
		defer ts.Close()

		// checks Solution for ID
		dl, err := newDownload(params)
		if assert.Error(t, err) {
			assert.Contains(t, err.Error(), "missing ID")
		}

		dl.Solution.ID = "1"
		// checks for error responses
		err = dl.validate()
		if assert.Error(t, err) {
			assert.Equal(t, err.Error(), "error-msg")
		}
	})
}

func TestDownloadParams(t *testing.T) {
	t.Run("creates from exercise", func(t *testing.T) {
		f := newFixture()
		got, err := newDownloadParamsFromExercise(f.usrCfg(), f.exercise())
		assert.NoError(t, err)

		want := f.downloadParams()

		assert.Equal(t, got.usrCfg, want.usrCfg)
		assert.Equal(t, got.slug, want.slug)
		assert.Equal(t, got.track, want.track)
	})

	t.Run("validates exercise", func(t *testing.T) {
		f := newFixture()
		exercise := f.exercise()

		_, err := newDownloadParamsFromExercise(f.usrCfg(), exercise)
		assert.NoError(t, err)

		exercise.Slug = ""
		_, err = newDownloadParamsFromExercise(f.usrCfg(), exercise)
		assert.Error(t, err)
	})

	t.Run("creates from flags", func(t *testing.T) {
		f := newFixture()
		got, err := newDownloadParamsFromFlags(f.usrCfg(), f.flags())
		assert.NoError(t, err)

		want := f.downloadParams()

		assert.Equal(t, got.usrCfg, want.usrCfg)
		assert.Equal(t, got.slug, want.slug)
		assert.Equal(t, got.track, want.track)
		assert.Equal(t, got.team, want.team)
	})

	t.Run("validates flags", func(t *testing.T) {
		f := newFixture()
		flags := f.flags()

		_, err := newDownloadParamsFromFlags(f.usrCfg(), flags)
		assert.NoError(t, err)

		// requires either exercise or uuid
		flags.Set("exercise", "")
		_, err = newDownloadParamsFromFlags(f.usrCfg(), flags)
		assert.Error(t, err)

		flags.Set("uuid", "bogus-uuid")
		_, err = newDownloadParamsFromFlags(f.usrCfg(), flags)
		assert.NoError(t, err)
	})

	t.Run("validates user config", func(t *testing.T) {
		f := newFixture()
		usrCfg := f.usrCfg()
		exercise := f.exercise()

		_, err := newDownloadParamsFromExercise(usrCfg, exercise)
		assert.NoError(t, err)

		usrCfg.Set("token", "")
		_, err = newDownloadParamsFromExercise(usrCfg, exercise)
		assert.Error(t, err)

		usrCfg = f.usrCfg()
		usrCfg.Set("workspace", "")
		_, err = newDownloadParamsFromExercise(usrCfg, exercise)
		assert.Error(t, err)

		usrCfg = f.usrCfg()
		usrCfg.Set("apibaseurl", "")
		_, err = newDownloadParamsFromExercise(usrCfg, exercise)
		assert.Error(t, err)
	})
}

func TestExercise(t *testing.T) {
	f := newFixture()
	var tests = []struct {
		desc        string
		team        string
		isRequestor bool
		expected    ws.Exercise
	}{
		{
			"team, is requestor", f.team, true,
			ws.Exercise{Root: filepath.Join(f.workspace, "teams", f.team), Track: f.track, Slug: f.slug},
		},
		{
			"no team, is requestor", "", true,
			ws.Exercise{Root: filepath.Join(f.workspace), Track: f.track, Slug: f.slug},
		},
		{
			"no team, not requestor", "", false,
			ws.Exercise{Root: filepath.Join(f.workspace, "users", f.handle), Track: f.track, Slug: f.slug},
		},
		{
			"team, not requestor", f.team, false,
			ws.Exercise{Root: filepath.Join(f.workspace, "teams", f.team, "users", f.handle), Track: f.track, Slug: f.slug},
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			dl, err := f.download()
			assert.NoError(t, err)

			dl.Solution.Team.Slug = tt.team
			dl.Solution.User.IsRequester = tt.isRequestor
			given := dl.exercise()

			assert.Equal(t, given, tt.expected)
		})
	}
}

func fakeDownloadServer(t *testing.T) *httptest.Server {
	f := newFixture()
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)

	// signal test error if route doesn't exist
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("missing handler for route '%s'", r.URL.Path)
	})

	mux.HandleFunc("/OK/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, f.payloadTmpl())
	})

	mux.HandleFunc("/unauthorized/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, f.errorTmpl())
	})

	mux.HandleFunc("/non200/", func(w http.ResponseWriter, r *http.Request) {
		// use 400 to fulfill a non 200 response
		w.WriteHeader(400)
		fmt.Fprintf(w, f.errorTmpl())
	})

	mux.HandleFunc("/errors/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, f.errorTmpl())
	})

	return server
}

// newDownloadServerRoutedTo returns a new test server with the given route configured.
func newDownloadServerRoutedTo(t *testing.T, usrCfg *viper.Viper, route string) *httptest.Server {
	t.Helper()
	ts := fakeDownloadServer(t)
	testServerURL := fmt.Sprintf("%s/%s", ts.URL, route)
	usrCfg.Set("apibaseurl", testServerURL)
	return ts
}

type fixture struct {
	workspace string
	handle    string
	team      string
	slug      string
	track     string
}

func newFixture() *fixture {
	return &fixture{
		workspace: "/home/username",
		handle:    "alice",
		team:      "bogus-team",
		slug:      "bogus-exercise",
		track:     "bogus-track",
	}
}

func (f *fixture) usrCfg() *viper.Viper {
	v := viper.New()
	v.Set("token", "abc123")
	v.Set("workspace", f.workspace)
	v.Set("apibaseurl", "http://example.com")
	return v
}

func (f *fixture) exercise() ws.Exercise {
	return ws.Exercise{
		Root:  f.payloadTmplDir(f.workspace),
		Track: f.track,
		Slug:  f.slug,
	}
}

func (f *fixture) flags() *pflag.FlagSet {
	flags := pflag.NewFlagSet("fake", pflag.PanicOnError)
	flags.String("uuid", "", "")
	flags.String("exercise", f.slug, "")
	flags.String("track", f.track, "")
	flags.String("team", f.team, "")
	return flags
}

func (f *fixture) downloadParams() *downloadParams {
	return &downloadParams{
		usrCfg: f.usrCfg(),
		slug:   f.slug,
		track:  f.track,
		team:   f.team,
	}
}

// download creates a new Download by unmarshaling the fixture template.
func (f *fixture) download() (*Download, error) {
	d := &Download{downloadParams: f.downloadParams()}
	d.downloadWriter = &downloadWriter{Download: d}
	if err := json.Unmarshal([]byte(f.payloadTmpl()), &d.downloadPayload); err != nil {
		return nil, err
	}
	return d, nil
}

// payloadTmplDir the directory resulting from the structure of fixture template.
func (f *fixture) payloadTmplDir(root string) string {
	return filepath.Join(root, "teams", f.team, "users", f.handle)
}

func (f *fixture) payloadTmpl() string {
	const tmpl = `
	{
		"solution": {
			"id": "bogus-id",
			"user": {
				"handle": "%s"
			},
			"team": {
				"name": "team-name",
				"slug": "%s"
			},
			"exercise": {
				"id": "%s",
				"instructions_url": "http://example.com/bogus-exercise",
				"auto_approve": false,
				"track": {
					"id": "%s",
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
				"/full/path/with/numeric-suffix/bogus-track/bogus-exercise-12345/subdir/numeric.txt",
				"empty"
			],
			"iteration": {
				"submitted_at": "2017-08-21t10:11:12.130z"
			}
		}
	}
	`
	return fmt.Sprintf(tmpl, f.handle, f.team, f.slug, f.track)
}

func (f *fixture) errorTmpl() string {
	return `
	{
	  "error": {
		"type": "bogus",
		"message": "error-msg",
		"possible_track_ids": []
	  }
	}
	`
}
