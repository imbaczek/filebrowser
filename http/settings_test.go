package fbhttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/asdine/storm/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/spf13/afero"

	"github.com/filebrowser/filebrowser/v2/img"
	"github.com/filebrowser/filebrowser/v2/settings"
	"github.com/filebrowser/filebrowser/v2/storage/bolt"
	"github.com/filebrowser/filebrowser/v2/users"
)

func TestSettingsPutHandler_ThumbnailMaxSourceImageDimensions(t *testing.T) {
	t.Parallel()

	key := []byte("test-key-test-key-test-key-test-key")

	newAdminAuthToken := func(t *testing.T, key []byte, u *users.User) string {
		t.Helper()

		now := time.Now()
		claims := authToken{
			User: userInfo{
				ID: u.ID,
			},
			RegisteredClaims: jwt.RegisteredClaims{
				IssuedAt:  jwt.NewNumericDate(now.Add(-1 * time.Minute)),
				ExpiresAt: jwt.NewNumericDate(now.Add(1 * time.Hour)),
			},
		}

		token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		s, err := token.SignedString(key)
		if err != nil {
			t.Fatalf("failed to sign token: %v", err)
		}
		return s
	}

	type putBody struct {
		Signup                bool                  `json:"signup"`
		HideLoginButton       bool                  `json:"hideLoginButton"`
		CreateUserDir         bool                  `json:"createUserDir"`
		MinimumPasswordLength uint                  `json:"minimumPasswordLength"`
		UserHomeBasePath      string                `json:"userHomeBasePath"`
		Defaults              settings.UserDefaults `json:"defaults"`
		Rules                 []any                 `json:"rules"`
		Branding              settings.Branding     `json:"branding"`
		Tus                   settings.Tus          `json:"tus"`
		Shell                 []string              `json:"shell"`
		Commands              map[string][]string   `json:"commands"`

		ThumbnailMaxSourceImageWidth  any `json:"thumbnailMaxSourceImageWidth,omitempty"`
		ThumbnailMaxSourceImageHeight any `json:"thumbnailMaxSourceImageHeight,omitempty"`
	}

	type want struct {
		status int
		w      uint
		h      uint
		size   uint
	}

	testCases := map[string]struct {
		body putBody
		want want
	}{
		"set 1x2": {
			body: putBody{
				ThumbnailMaxSourceImageWidth:  1,
				ThumbnailMaxSourceImageHeight: 2,
			},
			want: want{status: http.StatusOK, w: 1, h: 2, size: 0},
		},
		"set 2x1": {
			body: putBody{
				ThumbnailMaxSourceImageWidth:  2,
				ThumbnailMaxSourceImageHeight: 1,
			},
			want: want{status: http.StatusOK, w: 2, h: 1, size: 0},
		},
		"set 1x1": {
			body: putBody{
				ThumbnailMaxSourceImageWidth:  1,
				ThumbnailMaxSourceImageHeight: 1,
			},
			want: want{status: http.StatusOK, w: 1, h: 1, size: 1},
		},
		"reject 0 width": {
			body: putBody{
				ThumbnailMaxSourceImageWidth:  0,
				ThumbnailMaxSourceImageHeight: 1,
			},
			want: want{status: http.StatusBadRequest},
		},
		"reject 0 height": {
			body: putBody{
				ThumbnailMaxSourceImageWidth:  1,
				ThumbnailMaxSourceImageHeight: 0,
			},
			want: want{status: http.StatusBadRequest},
		},
		"reject negative width (decode)": {
			body: putBody{
				ThumbnailMaxSourceImageWidth:  -1,
				ThumbnailMaxSourceImageHeight: 1,
			},
			want: want{status: http.StatusBadRequest},
		},
		"reject negative height (decode)": {
			body: putBody{
				ThumbnailMaxSourceImageWidth:  1,
				ThumbnailMaxSourceImageHeight: -1,
			},
			want: want{status: http.StatusBadRequest},
		},
	}

	for name, tc := range testCases {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dbPath := filepath.Join(t.TempDir(), "db")
			db, err := storm.Open(dbPath)
			if err != nil {
				t.Fatalf("failed to open db: %v", err)
			}
			t.Cleanup(func() {
				_ = db.Close()
			})

			st, err := bolt.NewStorage(db)
			if err != nil {
				t.Fatalf("failed to get storage: %v", err)
			}

			if err := st.Settings.Save(&settings.Settings{Key: key}); err != nil {
				t.Fatalf("failed to save settings: %v", err)
			}

			admin := &users.User{
				Username: "admin",
				Password: "pw",
				Perm: users.Permissions{
					Admin: true,
				},
			}
			if err := st.Users.Save(admin); err != nil {
				t.Fatalf("failed to save admin user: %v", err)
			}

			// Ensure we have the stored ID for the auth token.
			admin, err = st.Users.Get("", "admin")
			if err != nil {
				t.Fatalf("failed to get admin user: %v", err)
			}

			st.Users = &customFSUser{
				Store: st.Users,
				fs:    &afero.MemMapFs{},
			}

			token := newAdminAuthToken(t, key, admin)

			// Minimal-ish payload for settings update; most fields are ignored by the code under test.
			bodyBytes, err := json.Marshal(tc.body)
			if err != nil {
				t.Fatalf("failed to marshal request: %v", err)
			}

			req, err := http.NewRequest(http.MethodPut, "/api/settings", bytes.NewReader(bodyBytes))
			if err != nil {
				t.Fatalf("failed to create request: %v", err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Auth", token)

			imgSvc := img.New(1)
			server := &settings.Server{
				Root:                          ".",
				ThumbnailMaxSourceImageWidth:  settings.DefaultThumbnailMaxSourceImageWidth,
				ThumbnailMaxSourceImageHeight: settings.DefaultThumbnailMaxSourceImageHeight,
			}

			recorder := httptest.NewRecorder()
			handler := handle(settingsPutHandler(imgSvc), "", st, server)
			handler.ServeHTTP(recorder, req)

			if recorder.Code != tc.want.status {
				t.Fatalf("expected status %d, got %d (body: %s)", tc.want.status, recorder.Code, recorder.Body.String())
			}

			if tc.want.status != http.StatusOK {
				return
			}

			ser, err := st.Settings.GetServer()
			if err != nil {
				t.Fatalf("failed to get server settings: %v", err)
			}

			if ser.ThumbnailMaxSourceImageWidth != tc.want.w {
				t.Fatalf("expected saved width %d, got %d", tc.want.w, ser.ThumbnailMaxSourceImageWidth)
			}
			if ser.ThumbnailMaxSourceImageHeight != tc.want.h {
				t.Fatalf("expected saved height %d, got %d", tc.want.h, ser.ThumbnailMaxSourceImageHeight)
			}
			if ser.ThumbnailMaxSourceImageSize != tc.want.size {
				t.Fatalf("expected saved size %d, got %d", tc.want.size, ser.ThumbnailMaxSourceImageSize)
			}
		})
	}
}
