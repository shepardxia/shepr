package chat

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const maxImageBytes = 24 << 20
const maxImagePixels = 32_000_000

var errImageTooLarge = fmt.Errorf("image exceeds %d MiB", maxImageBytes>>20)

type attachment struct {
	name, path    string
	width, height int
	thumb         *image.RGBA
}

func fileImage(dir, path string) (attachment, error) {
	// O_NONBLOCK lets us reject a FIFO without waiting for a writer.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return attachment{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return attachment{}, err
	}
	if !info.Mode().IsRegular() {
		return attachment{}, fmt.Errorf("%s is not a regular file", path)
	}
	if info.Size() > maxImageBytes {
		return attachment{}, errImageTooLarge
	}
	data, err := readImageBytes(f)
	if err != nil {
		return attachment{}, err
	}
	return storeImage(dir, filepath.Base(path), data)
}

func readImageBytes(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxImageBytes+1))
	if len(data) > maxImageBytes {
		return nil, errImageTooLarge
	}
	return data, err
}

// Store an immutable copy for delivery, retries and recaps. Only the thumbnail
// stays in memory. The room owns these files until its socket closes.
func storeImage(dir, name string, data []byte) (attachment, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return attachment{}, fmt.Errorf("expected a PNG, JPEG, GIF or WebP image: %w", err)
	}
	if cfg.Height <= 0 || cfg.Width <= 0 || cfg.Width > maxImagePixels/cfg.Height {
		return attachment{}, errors.New("image exceeds 32 megapixels or has invalid dimensions")
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return attachment{}, fmt.Errorf("could not decode image: %w", err)
	}
	scale := min(1.0, 48.0/float64(cfg.Width), 20.0/float64(cfg.Height))
	w := max(1, int(math.Round(float64(cfg.Width)*scale)))
	h := max(1, int(math.Round(float64(cfg.Height)*scale)))
	thumb := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.ApproxBiLinear.Scale(thumb, thumb.Bounds(), src, src.Bounds(), draw.Src, nil)

	path := filepath.Join(dir, fmt.Sprintf("%x.%s", sha256.Sum256(data), format))
	// Publish with a rename so concurrent posts of the same image never expose
	// a partially written file to an agent.
	f, err := os.CreateTemp(dir, "image-")
	if err != nil {
		return attachment{}, err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	closeErr := f.Close()
	if err != nil {
		return attachment{}, err
	}
	if closeErr != nil {
		return attachment{}, closeErr
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return attachment{}, err
	}
	name = strings.Join(strings.Fields(plainText(name)), " ")
	return attachment{name, path, cfg.Width, cfg.Height, thumb}, nil
}

func (a attachment) render(width int) string {
	label := fmt.Sprintf("▧ %s · %d×%d", a.name, a.width, a.height)
	uri := (&url.URL{Scheme: "file", Path: a.path}).String()
	label = ansi.SetHyperlink(uri) + label + ansi.ResetHyperlink()
	b := a.thumb.Bounds()
	w := min(width, b.Dx())
	h := max(1, b.Dy()*w/b.Dx())
	var out strings.Builder
	for y := 0; y < h; y += 2 {
		for x := 0; x < w; x++ {
			sample := func(row int) color.RGBA { return a.thumb.RGBAAt(x*b.Dx()/w, min(row, h-1)*b.Dy()/h) }
			// The two half-cells are independent pixels. Transparent areas
			// inherit the terminal background.
			top, bottom := sample(y), sample(y+1)
			style := lipgloss.NewStyle()
			switch {
			case top.A == 0 && bottom.A == 0:
				out.WriteByte(' ')
			case top.A == 0:
				out.WriteString(style.Foreground(bottom).Render("▄"))
			case bottom.A == 0:
				out.WriteString(style.Foreground(top).Render("▀"))
			default:
				out.WriteString(style.Foreground(top).Background(bottom).Render("▀"))
			}
		}
		out.WriteByte('\n')
	}
	return out.String() + lipgloss.Wrap(label, width, "")
}

var errNoClipboardImage = errors.New("clipboard has no image")

// Read a PNG from the macOS pasteboard without interpreting any clipboard text
// as a script. Screenshots may offer TIFF instead of PNG.
const pasteboardImage = `
ObjC.import('AppKit')
const pb = $.NSPasteboard.generalPasteboard
let data = pb.dataForType('public.png')
if (data.isNil()) {
    const tiff = pb.dataForType('public.tiff')
    if (!tiff.isNil()) {
        const rep = $.NSBitmapImageRep.imageRepWithData(tiff)
        if (!rep.isNil()) data = rep.representationUsingTypeProperties($.NSBitmapImageFileTypePNG, $({}))
    }
}
if (!data.isNil()) $.NSFileHandle.fileHandleWithStandardOutput.writeData(data)
void 0`

func clipboardImage(dir string) (attachment, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	name := "clipboard.png"
	if runtime.GOOS == "darwin" {
		cmd = exec.CommandContext(ctx, "osascript", "-l", "JavaScript", "-e", pasteboardImage)
	} else {
		var program string
		var list, read []string
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			program, list, read = "wl-paste", []string{"--list-types"}, []string{"--type"}
		} else {
			program, list, read = "xclip", []string{"-selection", "clipboard", "-o", "-t", "TARGETS"}, []string{"-selection", "clipboard", "-o", "-t"}
		}
		types, err := exec.CommandContext(ctx, program, list...).Output()
		if err != nil {
			return attachment{}, errNoClipboardImage
		}
		for _, mime := range strings.Fields(string(types)) {
			switch mime {
			case "image/png", "image/jpeg", "image/gif", "image/webp":
				name = "clipboard." + strings.TrimPrefix(mime, "image/")
				cmd = exec.CommandContext(ctx, program, append(read, mime)...)
			}
			if cmd != nil {
				break
			}
		}
		if cmd == nil {
			return attachment{}, errNoClipboardImage
		}
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return attachment{}, err
	}
	if err = cmd.Start(); err != nil {
		return attachment{}, err
	}
	data, err := readImageBytes(out)
	out.Close()
	waitErr := cmd.Wait()
	if err != nil {
		return attachment{}, err
	}
	if waitErr != nil {
		return attachment{}, fmt.Errorf("could not read clipboard image: %w", waitErr)
	}
	if len(data) == 0 {
		return attachment{}, errNoClipboardImage
	}
	return storeImage(dir, name, data)
}
