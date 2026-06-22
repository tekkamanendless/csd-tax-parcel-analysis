package demo

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"

	"github.com/go-app-blazar/blazar/blazar"
	"github.com/maxence-charriere/go-app/v11/pkg/app"
	"github.com/ncruces/go-sqlite3/util/ioutil"
	"github.com/ncruces/go-sqlite3/vfs"
	"github.com/ncruces/go-sqlite3/vfs/readervfs"
	"github.com/tekkamanendless/csd-tax-parcel-analysis/dataset"
	"github.com/tekkamanendless/csd-tax-parcel-analysis/internal/database"
)

type IndexPage struct {
	app.Compo
}

type EmbeddedFile struct {
	file   fs.File
	reader *bytes.Reader
}

var _ vfs.File = (*EmbeddedFile)(nil)

func NewFile(file fs.File) (*EmbeddedFile, error) {
	contents, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	reader := bytes.NewReader(contents)
	return &EmbeddedFile{file: file, reader: reader}, nil
}

func (f *EmbeddedFile) Close() error {
	slog.InfoContext(context.TODO(), "EmbeddedFile: Closing file")
	return nil
}

func (f *EmbeddedFile) ReadAt(p []byte, off int64) (int, error) {
	slog.InfoContext(context.TODO(), "EmbeddedFile: Reading file", "off", off, "len", len(p))
	return f.reader.ReadAt(p, off)
}

func (f *EmbeddedFile) WriteAt(p []byte, off int64) (int, error) {
	slog.InfoContext(context.TODO(), "EmbeddedFile: Writing file", "off", off, "len", len(p))
	return 0, errors.New("not implemented")
}

func (f *EmbeddedFile) Truncate(size int64) error {
	slog.InfoContext(context.TODO(), "EmbeddedFile: Truncating file", "size", size)
	return errors.New("not implemented")
}
func (f *EmbeddedFile) Sync(flags vfs.SyncFlag) error {
	slog.InfoContext(context.TODO(), "EmbeddedFile: Syncing file", "flags", flags)
	return nil
}
func (f *EmbeddedFile) Size() (int64, error) {
	slog.InfoContext(context.TODO(), "EmbeddedFile: Getting file size")
	return f.Size()
}
func (f *EmbeddedFile) Lock(lock vfs.LockLevel) error {
	slog.InfoContext(context.TODO(), "EmbeddedFile: Locking file", "lock", lock)
	return nil
}
func (f *EmbeddedFile) Unlock(lock vfs.LockLevel) error {
	slog.InfoContext(context.TODO(), "EmbeddedFile: Unlocking file", "lock", lock)
	return nil
}
func (f *EmbeddedFile) CheckReservedLock() (bool, error) {
	slog.InfoContext(context.TODO(), "EmbeddedFile: Checking reserved lock")
	return false, nil
}
func (f *EmbeddedFile) SectorSize() int {
	slog.InfoContext(context.TODO(), "EmbeddedFile: Getting sector size")
	return 512
}
func (f *EmbeddedFile) DeviceCharacteristics() vfs.DeviceCharacteristic {
	slog.InfoContext(context.TODO(), "EmbeddedFile: Getting device characteristics")
	return vfs.IOCAP_ATOMIC
}

type EmbeddedVFS struct {
	fs fs.FS
}

func (v *EmbeddedVFS) Open(name string, flags vfs.OpenFlag) (vfs.File, vfs.OpenFlag, error) {
	slog.InfoContext(context.TODO(), "EmbeddedVFS: Opening file", "name", name)

	file, err := v.fs.Open(name)
	if err != nil {
		return nil, vfs.OpenFlag(0), err
	}
	embeddedFile, err := NewFile(file)
	if err != nil {
		return nil, vfs.OpenFlag(0), err
	}
	return embeddedFile, vfs.OpenFlag(vfs.OPEN_READONLY), nil
}

func (v *EmbeddedVFS) Delete(name string, syncDir bool) error {
	return errors.New("not implemented")
}

func (v *EmbeddedVFS) Access(name string, flags vfs.AccessFlag) (bool, error) {
	slog.InfoContext(context.TODO(), "EmbeddedVFS: Acessing file", "name", name)

	file, err := v.fs.Open(name)
	if err != nil {
		return false, err
	}
	defer file.Close()
	return true, nil
}

func (v *EmbeddedVFS) FullPathname(name string) (string, error) {
	slog.InfoContext(context.TODO(), "EmbeddedVFS: Full pathname", "name", name)

	return name, nil
}

func (c *IndexPage) OnMount(ctx app.Context) {
	slog.InfoContext(ctx.Context, "IndexPage: OnMount")

	subFS, err := fs.Sub(dataset.EmbeddedFS, "embedded")
	if err != nil {
		slog.ErrorContext(ctx.Context, "Error creating sub FS", "err", err)
		return
	}
	//embeddedVFS := &EmbeddedVFS{fs: subFS}

	file, err := subFS.Open("database.county.sqlite.gz")
	if err != nil {
		slog.ErrorContext(ctx.Context, "Error opening file", "err", err)
		return
	}
	defer file.Close()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		slog.ErrorContext(ctx.Context, "Error creating gzip reader", "err", err)
		return
	}

	contents, err := io.ReadAll(gzipReader)
	if err != nil {
		slog.ErrorContext(ctx.Context, "Error reading file", "err", err)
		return
	}
	readervfs.Create("database.county.sqlite", ioutil.NewSizeReaderAt(bytes.NewReader(contents)))

	db, err := database.New(ctx.Context, "sqlite3", "file:database.county.sqlite?vfs=reader&cache=shared&parseTime=true")
	if err != nil {
		slog.ErrorContext(ctx.Context, "Error creating database", "err", err)
		return
	}

	type Row struct {
		Total int64 `gorm:"column:total"`
	}
	var row Row
	err = db.Raw("SELECT COUNT(*) AS total FROM parcel").Scan(&row).Error
	if err != nil {
		slog.ErrorContext(ctx.Context, "Error executing query", "err", err)
		return
	}
	slog.InfoContext(ctx.Context, "Total parcels", "total", row.Total)
}

func (c *IndexPage) OnNav(ctx app.Context) {
	slog.InfoContext(ctx.Context, "IndexPage: OnNav")
}

func (c *IndexPage) Render() app.UI {
	return blazar.Page().
		Body(
			app.Div().
				Body(
					app.Text("TODO"),
				),
		)
}
