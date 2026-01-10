package fbhttp

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/filebrowser/filebrowser/v2/rules"
	"github.com/filebrowser/filebrowser/v2/settings"
)

type settingsData struct {
	Signup                        bool                  `json:"signup"`
	HideLoginButton               bool                  `json:"hideLoginButton"`
	CreateUserDir                 bool                  `json:"createUserDir"`
	MinimumPasswordLength         uint                  `json:"minimumPasswordLength"`
	UserHomeBasePath              string                `json:"userHomeBasePath"`
	ThumbnailMaxSourceImageSize   uint                  `json:"thumbnailMaxSourceImageSize"`
	ThumbnailMaxSourceImageWidth  uint                  `json:"thumbnailMaxSourceImageWidth"`
	ThumbnailMaxSourceImageHeight uint                  `json:"thumbnailMaxSourceImageHeight"`
	Defaults                      settings.UserDefaults `json:"defaults"`
	AuthMethod                    settings.AuthMethod   `json:"authMethod"`
	Rules                         []rules.Rule          `json:"rules"`
	Branding                      settings.Branding     `json:"branding"`
	Tus                           settings.Tus          `json:"tus"`
	Shell                         []string              `json:"shell"`
	Commands                      map[string][]string   `json:"commands"`
}

var settingsGetHandler = withAdmin(func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
	data := &settingsData{
		Signup:                        d.settings.Signup,
		HideLoginButton:               d.settings.HideLoginButton,
		CreateUserDir:                 d.settings.CreateUserDir,
		MinimumPasswordLength:         d.settings.MinimumPasswordLength,
		UserHomeBasePath:              d.settings.UserHomeBasePath,
		ThumbnailMaxSourceImageSize:   d.server.ThumbnailMaxSourceImageSize,
		ThumbnailMaxSourceImageWidth:  d.server.ThumbnailMaxSourceImageWidth,
		ThumbnailMaxSourceImageHeight: d.server.ThumbnailMaxSourceImageHeight,
		Defaults:                      d.settings.Defaults,
		AuthMethod:                    d.settings.AuthMethod,
		Rules:                         d.settings.Rules,
		Branding:                      d.settings.Branding,
		Tus:                           d.settings.Tus,
		Shell:                         d.settings.Shell,
		Commands:                      d.settings.Commands,
	}

	return renderJSON(w, r, data)
})

type maxSourceImageSizer interface {
	SetMaxSourceImageDimensions(maxW, maxH uint)
}

type settingsUpdateRequest struct {
	Signup                bool                  `json:"signup"`
	HideLoginButton       bool                  `json:"hideLoginButton"`
	CreateUserDir         bool                  `json:"createUserDir"`
	MinimumPasswordLength uint                  `json:"minimumPasswordLength"`
	UserHomeBasePath      string                `json:"userHomeBasePath"`
	Defaults              settings.UserDefaults `json:"defaults"`
	Rules                 []rules.Rule          `json:"rules"`
	Branding              settings.Branding     `json:"branding"`
	Tus                   settings.Tus          `json:"tus"`
	Shell                 []string              `json:"shell"`
	Commands              map[string][]string   `json:"commands"`

	ThumbnailMaxSourceImageSize   *uint `json:"thumbnailMaxSourceImageSize"`
	ThumbnailMaxSourceImageWidth  *uint `json:"thumbnailMaxSourceImageWidth"`
	ThumbnailMaxSourceImageHeight *uint `json:"thumbnailMaxSourceImageHeight"`
}

func settingsPutHandler(imgSvc ImgService) handleFunc {
	return withAdmin(func(_ http.ResponseWriter, r *http.Request, d *data) (int, error) {
		req := &settingsUpdateRequest{}
		err := json.NewDecoder(r.Body).Decode(req)
		if err != nil {
			return http.StatusBadRequest, err
		}

		d.settings.Signup = req.Signup
		d.settings.CreateUserDir = req.CreateUserDir
		d.settings.MinimumPasswordLength = req.MinimumPasswordLength
		d.settings.UserHomeBasePath = req.UserHomeBasePath
		d.settings.Defaults = req.Defaults
		d.settings.Rules = req.Rules
		d.settings.Branding = req.Branding
		d.settings.Tus = req.Tus
		d.settings.Shell = req.Shell
		d.settings.Commands = req.Commands
		d.settings.HideLoginButton = req.HideLoginButton

		maxW := d.server.ThumbnailMaxSourceImageWidth
		maxH := d.server.ThumbnailMaxSourceImageHeight

		if req.ThumbnailMaxSourceImageSize != nil {
			if *req.ThumbnailMaxSourceImageSize < 1 {
				return http.StatusBadRequest, errors.New("thumbnailMaxSourceImageSize must be >= 1")
			}
			maxW = *req.ThumbnailMaxSourceImageSize
			maxH = *req.ThumbnailMaxSourceImageSize
		}

		if req.ThumbnailMaxSourceImageWidth != nil {
			if *req.ThumbnailMaxSourceImageWidth < 1 {
				return http.StatusBadRequest, errors.New("thumbnailMaxSourceImageWidth must be >= 1")
			}
			maxW = *req.ThumbnailMaxSourceImageWidth
		}

		if req.ThumbnailMaxSourceImageHeight != nil {
			if *req.ThumbnailMaxSourceImageHeight < 1 {
				return http.StatusBadRequest, errors.New("thumbnailMaxSourceImageHeight must be >= 1")
			}
			maxH = *req.ThumbnailMaxSourceImageHeight
		}

		if maxW == 0 {
			maxW = settings.DefaultThumbnailMaxSourceImageWidth
		}
		if maxH == 0 {
			maxH = settings.DefaultThumbnailMaxSourceImageHeight
		}

		d.server.ThumbnailMaxSourceImageWidth = maxW
		d.server.ThumbnailMaxSourceImageHeight = maxH
		if maxW == maxH {
			d.server.ThumbnailMaxSourceImageSize = maxW
		} else {
			d.server.ThumbnailMaxSourceImageSize = 0
		}

		err = d.store.Settings.Save(d.settings)
		if err != nil {
			return errToStatus(err), err
		}

		err = d.store.Settings.SaveServer(d.server)
		if err != nil {
			return errToStatus(err), err
		}

		if s, ok := imgSvc.(maxSourceImageSizer); ok {
			s.SetMaxSourceImageDimensions(d.server.ThumbnailMaxSourceImageWidth, d.server.ThumbnailMaxSourceImageHeight)
		}

		return 0, nil
	})
}
