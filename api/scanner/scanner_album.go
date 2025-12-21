package scanner

import (
	"fmt"
	"os"
	"path"
	"time"

	"github.com/photoview/photoview/api/graphql/models"
	"github.com/photoview/photoview/api/log"
	"github.com/photoview/photoview/api/scanner/media_encoding"
	"github.com/photoview/photoview/api/scanner/scanner_task"
	"github.com/photoview/photoview/api/scanner/scanner_tasks"
	"github.com/photoview/photoview/api/scanner/scanner_utils"
	"github.com/photoview/photoview/api/utils"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

func NewRootAlbum(db *gorm.DB, rootPath string, owner *models.User) (*models.Album, error) {

	if !ValidRootPath(rootPath) {
		return nil, ErrorInvalidRootPath
	}

	if !path.IsAbs(rootPath) {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}

		rootPath = path.Join(wd, rootPath)
	}

	owners := []models.User{
		*owner,
	}

	var matchedAlbums []models.Album
	if err := db.Where("path_hash = ?", models.MD5Hash(rootPath)).Find(&matchedAlbums).Error; err != nil {
		return nil, err
	}

	if len(matchedAlbums) > 0 {
		album := matchedAlbums[0]

		var matchedUserAlbumCount int64
		if err := db.Table("user_albums").Where("user_id = ?", owner.ID).Where("album_id = ?", album.ID).Count(&matchedUserAlbumCount).Error; err != nil {
			return nil, err
		}

		if matchedUserAlbumCount > 0 {
			return nil, errors.New(fmt.Sprintf("user already owns a path containing this path: %s", rootPath))
		}

		if err := db.Model(&owner).Association("Albums").Append(&album); err != nil {
			return nil, errors.Wrap(err, "add owner to already existing album")
		}

		return &album, nil
	} else {
		album := models.Album{
			Title:  path.Base(rootPath),
			Path:   rootPath,
			Owners: owners,
		}

		if err := db.Create(&album).Error; err != nil {
			return nil, err
		}

		return &album, nil
	}
}

var ErrorInvalidRootPath = errors.New("invalid root path")

func ValidRootPath(rootPath string) bool {
	_, err := os.Stat(rootPath)
	if err != nil {
		log.Warn(nil, "invalid root path", "root_path", rootPath, "error", err)
		return false
	}

	return true
}

func ScanAlbum(ctx scanner_task.TaskContext) error {
	newCtx, err := scanner_tasks.Tasks.BeforeScanAlbum(ctx)
	if err != nil {
		return errors.Wrapf(err, "before scan album (%s)", ctx.GetAlbum().Path)
	}
	ctx = newCtx

	// Scan for photos
	albumMedia, err := findMediaForAlbum(ctx)
	if err != nil {
		return errors.Wrapf(err, "find media for album (%s): %s", ctx.GetAlbum().Path, err)
	}

	changedMedia := make([]*models.Media, 0)
	for i, media := range albumMedia {
		mediaData := media_encoding.NewEncodeMediaData(media)

		if err := scanMedia(ctx, media, &mediaData, i, len(albumMedia)); err != nil {
			scanner_utils.ScannerError(ctx, "Error scanning media for album (%d) file (%s): %s\n", ctx.GetAlbum().ID, media.Path, err)
		}
	}

	if err := scanner_tasks.Tasks.AfterScanAlbum(ctx, changedMedia, albumMedia); err != nil {
		return errors.Wrap(err, "after scan album")
	}

	return nil
}
func findMediaForAlbum(ctx scanner_task.TaskContext) ([]*models.Media, error) {
	start := time.Now()

	albumMedia := make([]*models.Media, 0)

	album := ctx.GetAlbum()
	albumPath := album.Path
	albumID := album.ID

	dirContent, err := os.ReadDir(albumPath)
	if err != nil {
		return nil, err
	}

	var (
		totalItems int
		candidates int
		scanned    int
		skipped    int
		scanErrors int
	)

	// Timings
	var (
		symlinkTime     time.Duration
		fileInfoTime    time.Duration
		mediaTaskTime   time.Duration
		dbScanTime      time.Duration
		mediaPathTime   time.Duration
		ifConditionTime time.Duration
	)

	for _, item := range dirContent {
		totalItems++

		mediaPathStart := time.Now()
		mediaPath := path.Join(albumPath, item.Name())
		mediaPathTime += time.Since(mediaPathStart)

		// --- Check if dir symlink ---
		symlinkStart := time.Now()
		isDirSymlink, err := utils.IsDirSymlink(mediaPath)
		if err != nil {
			log.Warn(ctx, "Cannot detect whether the path is symlink to a directory. Pretending it is not", "media_path", mediaPath)
			isDirSymlink = false
		}
		symlinkTime += time.Since(symlinkStart)

		if item.IsDir() || isDirSymlink {
			continue
		}

		if !ctx.GetCache().IsPathMediaEntry(item, mediaPath) {
			continue
		}

		candidates++

		// --- Get file info ---
		fileInfoStart := time.Now()
		itemInfo, err := item.Info()
		fileInfoTime += time.Since(fileInfoStart)
		if err != nil {
			return nil, err
		}

		// --- Media task check ---
		mediaTaskStart := time.Now()
		skip, err := scanner_tasks.Tasks.MediaFound(ctx, itemInfo, mediaPath)
		mediaTaskTime += time.Since(mediaTaskStart)
		if err != nil {
			return nil, err
		}
		if skip {
			skipped++
			continue
		}

		// --- Database scan & media processing ---
		dbScanStart := time.Now()
		err = ctx.DatabaseTransaction(func(ctx scanner_task.TaskContext) error {
			media, isNewMedia, err := ScanMedia(ctx.GetDB(), mediaPath, albumID, ctx.GetCache())
			if err != nil {
				return errors.Wrapf(err, "scanning media error (%s)", mediaPath)
			}

			if err = scanner_tasks.Tasks.AfterMediaFound(ctx, media, isNewMedia); err != nil {
				return err
			}

			albumMedia = append(albumMedia, media)
			scanned++
			return nil
		})
		dbScanTime += time.Since(dbScanStart)

		if err != nil {
			scanErrors++
			scanner_utils.ScannerError(ctx, "Error scanning media for album (%d): %s\n", albumID, err)
			continue
		}
	}

	elapsed := time.Since(start)

	log.Info(
		ctx,
		"Finished scanning album media",
		"album_path", albumPath,
		"album_id", albumID,
		"total_items", totalItems,
		"candidates", candidates,
		"scanned", scanned,
		"skipped", skipped,
		"errors", scanErrors,
		"duration", elapsed,
		"timing_symlink", symlinkTime,
		"timing_file_info", fileInfoTime,
		"timing_media_task", mediaTaskTime,
		"timing_db_scan", dbScanTime,
		"timing_mediaPathTime", mediaPathTime,
		"timing_ifCondition", ifConditionTime,
	)

	if err != nil {
		scanner_utils.ScannerError(
			ctx,
			"Error scanning media for album (%d): %s\n",
			albumID,
			err,
		)
		return nil, err
	}

	return albumMedia, nil
}

func processMedia(ctx scanner_task.TaskContext, mediaData *media_encoding.EncodeMediaData) ([]*models.MediaURL, error) {

	// Make sure media cache directory exists
	mediaCachePath, err := mediaData.Media.CachePath()
	if err != nil {
		return []*models.MediaURL{}, errors.Wrap(err, "cache directory error")
	}

	return scanner_tasks.Tasks.ProcessMedia(ctx, mediaData, mediaCachePath)
}
