package gozxing

import (
	"fmt"
	"image"

	zxing "github.com/makiuchi-d/gozxing"
)

func newLuminanceSource(img image.Image) zxing.LuminanceSource {
	switch src := img.(type) {
	case *image.Gray:
		return grayLuminanceSource(src)
	case *image.YCbCr:
		return ycbcrLuminanceSource(src)
	case *image.RGBA:
		return rgbaLuminanceSource(src)
	case *image.NRGBA:
		return nrgbaLuminanceSource(src)
	default:
		return zxing.NewLuminanceSourceFromImage(img)
	}
}

func grayLuminanceSource(img *image.Gray) zxing.LuminanceSource {
	rect := img.Bounds()
	width := rect.Dx()
	height := rect.Dy()
	luminance := make([]byte, width*height)
	index := 0
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		offset := img.PixOffset(rect.Min.X, y)
		copy(luminance[index:index+width], img.Pix[offset:offset+width])
		index += width
	}
	return newPackedLuminanceSource(width, height, luminance)
}

func ycbcrLuminanceSource(img *image.YCbCr) zxing.LuminanceSource {
	rect := img.Bounds()
	width := rect.Dx()
	height := rect.Dy()
	luminance := make([]byte, width*height)
	index := 0
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			luminance[index] = img.Y[img.YOffset(x, y)]
			index++
		}
	}
	return newPackedLuminanceSource(width, height, luminance)
}

func rgbaLuminanceSource(img *image.RGBA) zxing.LuminanceSource {
	rect := img.Bounds()
	width := rect.Dx()
	height := rect.Dy()
	luminance := make([]byte, width*height)
	index := 0
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		offset := img.PixOffset(rect.Min.X, y)
		row := img.Pix[offset : offset+width*4]
		for x := 0; x < width; x++ {
			base := x * 4
			r := uint32(row[base])
			g := uint32(row[base+1])
			b := uint32(row[base+2])
			a := uint32(row[base+3])
			lum := (r + 2*g + b) / 4
			luminance[index] = byte((lum*a + (255-a)*255) / 255)
			index++
		}
	}
	return newPackedLuminanceSource(width, height, luminance)
}

func nrgbaLuminanceSource(img *image.NRGBA) zxing.LuminanceSource {
	rect := img.Bounds()
	width := rect.Dx()
	height := rect.Dy()
	luminance := make([]byte, width*height)
	index := 0
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		offset := img.PixOffset(rect.Min.X, y)
		row := img.Pix[offset : offset+width*4]
		for x := 0; x < width; x++ {
			base := x * 4
			r := uint32(row[base])
			g := uint32(row[base+1])
			b := uint32(row[base+2])
			a := uint32(row[base+3])
			lum := (r + 2*g + b) / 4
			luminance[index] = byte((lum*a + (255-a)*255) / 255)
			index++
		}
	}
	return newPackedLuminanceSource(width, height, luminance)
}

func newPackedLuminanceSource(width, height int, luminance []byte) zxing.LuminanceSource {
	return &packedLuminanceSource{
		LuminanceSourceBase: zxing.LuminanceSourceBase{Width: width, Height: height},
		luminances:          luminance,
		dataWidth:           width,
		dataHeight:          height,
	}
}

type packedLuminanceSource struct {
	zxing.LuminanceSourceBase
	luminances []byte
	dataWidth  int
	dataHeight int
	left       int
	top        int
}

func (s *packedLuminanceSource) GetRow(y int, row []byte) ([]byte, error) {
	if y < 0 || y >= s.GetHeight() {
		return row, fmt.Errorf("requested row %d is outside the image", y)
	}
	width := s.GetWidth()
	if row == nil || len(row) < width {
		row = make([]byte, width)
	}
	offset := (y+s.top)*s.dataWidth + s.left
	copy(row, s.luminances[offset:offset+width])
	return row, nil
}

func (s *packedLuminanceSource) GetMatrix() []byte {
	width := s.GetWidth()
	height := s.GetHeight()
	if width == s.dataWidth && height == s.dataHeight {
		return s.luminances
	}

	matrix := make([]byte, width*height)
	inputOffset := s.top*s.dataWidth + s.left
	if width == s.dataWidth {
		copy(matrix, s.luminances[inputOffset:inputOffset+len(matrix)])
		return matrix
	}
	for y := 0; y < height; y++ {
		outputOffset := y * width
		copy(matrix[outputOffset:outputOffset+width], s.luminances[inputOffset:inputOffset+width])
		inputOffset += s.dataWidth
	}
	return matrix
}

func (s *packedLuminanceSource) IsCropSupported() bool {
	return true
}

func (s *packedLuminanceSource) Crop(left, top, width, height int) (zxing.LuminanceSource, error) {
	if left+width > s.dataWidth || top+height > s.dataHeight {
		return nil, fmt.Errorf("crop rectangle does not fit within image data")
	}
	return &packedLuminanceSource{
		LuminanceSourceBase: zxing.LuminanceSourceBase{Width: width, Height: height},
		luminances:          s.luminances,
		dataWidth:           s.dataWidth,
		dataHeight:          s.dataHeight,
		left:                s.left + left,
		top:                 s.top + top,
	}, nil
}

func (s *packedLuminanceSource) Invert() zxing.LuminanceSource {
	return zxing.LuminanceSourceInvert(s)
}

func (s *packedLuminanceSource) String() string {
	return zxing.LuminanceSourceString(s)
}
