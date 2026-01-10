//go:generate go-enum --sql --marshal --file $GOFILE
package img

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"sync/atomic"

	"github.com/disintegration/imaging"
	"github.com/dsoprea/go-exif/v3"
	"github.com/marusama/semaphore/v2"

	exifcommon "github.com/dsoprea/go-exif/v3/common"
)

// ErrUnsupportedFormat means the given image format is not supported.
var ErrUnsupportedFormat = errors.New("unsupported image format")

// ErrImageTooLarge means the image is too large to create a thumbnail.
var ErrImageTooLarge = errors.New("image too large for thumbnail generation")

// DefaultMaxSourceImageSize is the legacy default maximum image dimension (width/height) that will be decoded for thumbnail generation.
const DefaultMaxSourceImageSize = 10000

const DefaultMaxSourceImageWidth = 10000
const DefaultMaxSourceImageHeight = 10000

// Service
type Service struct {
	sem                semaphore.Semaphore
	maxSourceImgWidth  atomic.Int64
	maxSourceImgHeight atomic.Int64
}

func New(workers int) *Service {
	s := &Service{
		sem: semaphore.New(workers),
	}
	s.maxSourceImgWidth.Store(DefaultMaxSourceImageWidth)
	s.maxSourceImgHeight.Store(DefaultMaxSourceImageHeight)
	return s
}

// SetMaxSourceImageSize sets the maximum source image dimension (width/height) allowed for thumbnail generation.
// A value of 0 resets it to DefaultMaxSourceImageSize.
func (s *Service) SetMaxSourceImageSize(max uint) {
	s.SetMaxSourceImageDimensions(max, max)
}

// SetMaxSourceImageDimensions sets the maximum source image width/height allowed for thumbnail generation.
// A value of 0 resets the respective dimension to its default.
func (s *Service) SetMaxSourceImageDimensions(maxW, maxH uint) {
	s.maxSourceImgWidth.Store(int64(s.sanitizeMaxDim(maxW, DefaultMaxSourceImageWidth)))
	s.maxSourceImgHeight.Store(int64(s.sanitizeMaxDim(maxH, DefaultMaxSourceImageHeight)))
}

func (s *Service) sanitizeMaxDim(v uint, def int) int {
	if v == 0 {
		return def
	}
	maxInt := int(^uint(0) >> 1)
	if uint64(v) > uint64(maxInt) {
		return def
	}
	return int(v)
}

func (s *Service) maxSourceImageDimensions() (int, int) {
	maxW := s.maxSourceImgWidth.Load()
	maxH := s.maxSourceImgHeight.Load()

	if maxW <= 0 {
		maxW = DefaultMaxSourceImageWidth
	}
	if maxH <= 0 {
		maxH = DefaultMaxSourceImageHeight
	}

	maxInt := int64(^uint(0) >> 1)
	if maxW > maxInt {
		maxW = maxInt
	}
	if maxH > maxInt {
		maxH = maxInt
	}

	return int(maxW), int(maxH)
}

// Format is an image file format.
/*
ENUM(
jpeg
png
gif
tiff
bmp
)
*/
type Format int

func (x Format) toImaging() imaging.Format {
	switch x {
	case FormatJpeg:
		return imaging.JPEG
	case FormatPng:
		return imaging.PNG
	case FormatGif:
		return imaging.GIF
	case FormatTiff:
		return imaging.TIFF
	case FormatBmp:
		return imaging.BMP
	default:
		return imaging.JPEG
	}
}

/*
ENUM(
high
medium
low
)
*/
type Quality int

func (x Quality) resampleFilter() imaging.ResampleFilter {
	switch x {
	case QualityHigh:
		return imaging.Lanczos
	case QualityMedium:
		return imaging.Box
	case QualityLow:
		return imaging.NearestNeighbor
	default:
		return imaging.Box
	}
}

/*
ENUM(
fit
fill
)
*/
type ResizeMode int

func (s *Service) FormatFromExtension(ext string) (Format, error) {
	format, err := imaging.FormatFromExtension(ext)
	if err != nil {
		return -1, ErrUnsupportedFormat
	}
	switch format {
	case imaging.JPEG:
		return FormatJpeg, nil
	case imaging.PNG:
		return FormatPng, nil
	case imaging.GIF:
		return FormatGif, nil
	case imaging.TIFF:
		return FormatTiff, nil
	case imaging.BMP:
		return FormatBmp, nil
	}
	return -1, ErrUnsupportedFormat
}

type resizeConfig struct {
	format     Format
	resizeMode ResizeMode
	quality    Quality
}

type Option func(*resizeConfig)

func WithFormat(format Format) Option {
	return func(config *resizeConfig) {
		config.format = format
	}
}

func WithMode(mode ResizeMode) Option {
	return func(config *resizeConfig) {
		config.resizeMode = mode
	}
}

func WithQuality(quality Quality) Option {
	return func(config *resizeConfig) {
		config.quality = quality
	}
}

func (s *Service) Resize(ctx context.Context, in io.Reader, width, height int, out io.Writer, options ...Option) error {
	if err := s.sem.Acquire(ctx, 1); err != nil {
		return err
	}
	defer s.sem.Release(1)

	format, wrappedReader, err := s.detectFormat(in)
	if err != nil {
		return err
	}

	config := resizeConfig{
		format:     format,
		resizeMode: ResizeModeFit,
		quality:    QualityMedium,
	}
	for _, option := range options {
		option(&config)
	}

	if config.quality == QualityLow && format == FormatJpeg {
		thm, newWrappedReader, errThm := getEmbeddedThumbnail(wrappedReader)
		wrappedReader = newWrappedReader
		if errThm == nil {
			_, err = out.Write(thm)
			if err == nil {
				return nil
			}
		}
	}

	img, err := imaging.Decode(wrappedReader, imaging.AutoOrientation(true))
	if err != nil {
		return err
	}

	switch config.resizeMode {
	case ResizeModeFill:
		img = imaging.Fill(img, width, height, imaging.Center, config.quality.resampleFilter())
	case ResizeModeFit:
		fallthrough
	default:
		img = imaging.Fit(img, width, height, config.quality.resampleFilter())
	}

	return imaging.Encode(out, img, config.format.toImaging())
}

func (s *Service) detectFormat(in io.Reader) (Format, io.Reader, error) {
	buf := &bytes.Buffer{}
	r := io.TeeReader(in, buf)

	imgConfig, imgFormat, err := image.DecodeConfig(r)
	if err != nil {
		return 0, nil, fmt.Errorf("%s: %w", err.Error(), ErrUnsupportedFormat)
	}

	// Check if image dimensions exceed maximum allowed size
	maxW, maxH := s.maxSourceImageDimensions()
	if imgConfig.Width > maxW || imgConfig.Height > maxH {
		return 0, nil, fmt.Errorf("image dimensions %dx%d exceed maximum %dx%d: %w",
			imgConfig.Width, imgConfig.Height, maxW, maxH, ErrImageTooLarge)
	}

	format, err := ParseFormat(imgFormat)
	if err != nil {
		return 0, nil, ErrUnsupportedFormat
	}

	return format, io.MultiReader(buf, in), nil
}

func getEmbeddedThumbnail(in io.Reader) ([]byte, io.Reader, error) {
	buf := &bytes.Buffer{}
	r := io.TeeReader(in, buf)
	wrappedReader := io.MultiReader(buf, in)

	offset := 0
	offsets := []int{12, 30}
	head := make([]byte, 0xffff)

	_, err := r.Read(head)
	if err != nil {
		return nil, wrappedReader, err
	}

	for _, offset = range offsets {
		if _, err = exif.ParseExifHeader(head[offset:]); err == nil {
			break
		}
	}

	if err != nil {
		return nil, wrappedReader, err
	}

	im, err := exifcommon.NewIfdMappingWithStandard()
	if err != nil {
		return nil, wrappedReader, err
	}

	_, index, err := exif.Collect(im, exif.NewTagIndex(), head[offset:])
	if err != nil {
		return nil, wrappedReader, err
	}

	ifd := index.RootIfd.NextIfd()
	if ifd == nil {
		return nil, wrappedReader, exif.ErrNoThumbnail
	}

	thm, err := ifd.Thumbnail()
	return thm, wrappedReader, err
}
