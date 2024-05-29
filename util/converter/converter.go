package converter

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"moredoc/util"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mnt-ltd/command"

	"github.com/gofrs/uuid"
	"go.uber.org/zap"
	"rsc.io/pdf"
)

const (
	soffice      = "soffice"
	ebookConvert = "ebook-convert"
	svgo         = "svgo"
	mutool       = "mutool"
	inkscape     = "inkscape"
	dirDteFmt    = "2006/01/02"
	imageMagick  = "convert"
)

type ConvertCallback func(page int, pagePath string, err error)

type Converter struct {
	cachePath string
	timeout   time.Duration
	logger    *zap.Logger
	workspace string
}

type Page struct {
	PageNum  int
	PagePath string
}

// NewConverter 创建一个新的转换器。每个不同的原始文档，都需要一个新的转换器。
// 因为最后可以清空该原始文档及其衍生文件的临时目录，用节省磁盘空间。
func NewConverter(logger *zap.Logger, timeout ...time.Duration) *Converter {
	expire := 1 * time.Hour
	if len(timeout) > 0 {
		expire = timeout[0]
	}
	defaultCachePath := "cache/convert"
	cvt := &Converter{
		cachePath: defaultCachePath,
		timeout:   expire,
		logger:    logger.Named("converter"),
	}
	cvt.workspace = cvt.makeWorkspace()
	os.MkdirAll(defaultCachePath, os.ModePerm)
	os.MkdirAll(cvt.workspace, os.ModePerm)
	return cvt
}

func (c *Converter) SetCachePath(cachePath string) {
	os.MkdirAll(cachePath, os.ModePerm)
	c.cachePath = cachePath
}

// ConvertToPDF 将文件转为PDF。
// 自动根据文件类型调用相应的转换函数。
func (c *Converter) ConvertToPDF(src string) (dst string, err error) {
	ext := strings.ToLower(filepath.Ext(src))
	switch ext {
	case ".epub":
		return c.ConvertEPUBToPDF(src)
	case ".umd":
		return c.ConvertUMDToPDF(src)
	case ".txt":
		return c.ConvertTXTToPDF(src)
	case ".mobi":
		return c.ConvertMOBIToPDF(src)
	case ".azw", ".azw3", ".azw4":
		return c.ConvertAZWToPDF(src)
	case ".chm":
		return c.ConvertCHMToPDF(src)
	case ".pdf":
		return c.PDFToPDF(src)
	// case ".doc", ".docx", ".rtf", ".wps", ".odt",
	// 	".xls", ".xlsx", ".et", ".ods",
	// 	".ppt", ".pptx", ".dps", ".odp", ".pps", ".ppsx", ".pot", ".potx":
	// 	return c.ConvertOfficeToPDF(src)
	default:
		return c.ConvertOfficeToPDF(src)
		// return "", fmt.Errorf("不支持的文件类型：%s", ext)
	}
}

// ConvertOfficeToPDF 通过soffice将office文档转换为pdf
func (c *Converter) ConvertOfficeToPDF(src string) (dst string, err error) {
	return c.convertToPDFBySoffice(src)
}

// ConvertEPUBToPDF 将 epub 转为PDF
func (c *Converter) ConvertEPUBToPDF(src string) (dst string, err error) {
	return c.convertToPDFByCalibre(src)
}

// ConvertUMDToPDF 将umd转为PDF
func (c *Converter) ConvertUMDToPDF(src string) (dst string, err error) {
	return c.convertToPDFBySoffice(src)
}

// ConvertTXTToPDF 将 txt 转为PDF
func (c *Converter) ConvertTXTToPDF(src string) (dst string, err error) {
	return c.convertToPDFBySoffice(src)
}

// ConvertMOBIToPDF 将 mobi 转为PDF
func (c *Converter) ConvertMOBIToPDF(src string) (dst string, err error) {
	return c.convertToPDFByCalibre(src)
}

func (c *Converter) ConvertAZWToPDF(src string) (dst string, err error) {
	return c.convertToPDFByCalibre(src)
}

// ConvertPDFToTxt 将PDF转为TXT
func (c *Converter) ConvertPDFToTxt(src string) (dst string, err error) {
	dst = c.workspace + "/dst.txt"
	args := []string{
		"convert",
		"-o",
		dst,
		src,
	}
	c.logger.Info("convert pdf to txt", zap.String("cmd", mutool), zap.Strings("args", args))
	_, err = command.ExecCommand(mutool, args, c.timeout)
	if err != nil {
		c.logger.Error("convert pdf to txt", zap.String("cmd", mutool), zap.Strings("args", args), zap.Error(err))
		return
	}
	return dst, nil
}

// ConvertCHMToPDF 将CHM转为PDF
func (c *Converter) ConvertCHMToPDF(src string) (dst string, err error) {
	return c.convertToPDFByCalibre(src)
}

// ConvertPDFToSVG 将PDF转为SVG
func (c *Converter) ConvertPDFToSVG(src string, fromPage, toPage int, enableSVGO, enableGZIP bool) (pages []Page, err error) {
	pages, err = c.convertPDFToPage(src, fromPage, toPage, ".svg")
	if err != nil {
		return
	}

	if len(pages) == 0 {
		return
	}

	if enableSVGO { // 压缩svg
		c.CompressSVGBySVGO(filepath.Dir(pages[0].PagePath))
	}

	if enableGZIP { // gzip 压缩
		for idx, page := range pages {
			if dst, errCompress := c.CompressSVGByGZIP(page.PagePath); errCompress == nil {
				os.Remove(page.PagePath)
				page.PagePath = dst
				pages[idx] = page
			}
		}
	}
	return
}

type OptionConvertPages struct {
	EnableSVGO bool
	EnableGZIP bool
	Extension  string
}

// ConvertPDFToPages 将PDF转为预览页
func (c *Converter) ConvertPDFToPages(src string, fromPage, toPage int, option *OptionConvertPages) (pages []Page, err error) {
	ext := strings.TrimLeft(option.Extension, ".")
	switch ext {
	case "png":
		return c.ConvertPDFToPNG(src, fromPage, toPage)
	case "jpg", "webp":
		// 见将pdf转为png，然后png再转为jpg
		pages, err = c.ConvertPDFToPNG(src, fromPage, toPage)
		// 通过imagemagick将图片转为jpg
		var (
			dst        string
			errConvert error
		)
		for idx, page := range pages {
			if ext != "webp" {
				dst, errConvert = c.ConvertPNGToJPG(page.PagePath)
			} else {
				dst, errConvert = c.ConvertPNGToWEBP(page.PagePath)
			}
			if errConvert == nil {
				os.Remove(page.PagePath)
				page.PagePath = dst
				pages[idx] = page
			}
		}
		return pages, err
	default: // 默认转为svg
		return c.ConvertPDFToSVG(src, fromPage, toPage, option.EnableSVGO, option.EnableGZIP)
	}
}

// 将png转为jpg
func (c *Converter) ConvertPNGToJPG(src string) (dst string, err error) {
	dst = strings.TrimSuffix(src, filepath.Ext(src)) + ".jpg"
	// 通过imagemagick将图片转为jpg
	args := []string{
		src,
		dst,
	}
	c.logger.Debug("convert png to jpg", zap.String("cmd", imageMagick), zap.Strings("args", args))
	_, err = command.ExecCommand(imageMagick, args, c.timeout)
	if err != nil {
		c.logger.Error("convert png to jpg", zap.String("cmd", imageMagick), zap.Strings("args", args), zap.Error(err))
	}
	return
}

// 将png转为webp
func (c *Converter) ConvertPNGToWEBP(src string) (dst string, err error) {
	dst = strings.TrimSuffix(src, filepath.Ext(src)) + ".webp"
	// 通过imagemagick将图片转为jpg
	args := []string{
		src,
		dst,
	}
	c.logger.Debug("convert png to webp", zap.String("cmd", imageMagick), zap.Strings("args", args))
	_, err = command.ExecCommand(imageMagick, args, c.timeout)
	if err != nil {
		c.logger.Error("convert png to webp", zap.String("cmd", imageMagick), zap.Strings("args", args), zap.Error(err))
	}
	return
}

func (c *Converter) ConvertPDFToJPG(src string, fromPage, toPage int) (pages []Page, err error) {
	return c.convertPDFToPage(src, fromPage, toPage, ".jpg")
}

// ConvertPDFToPNG 将PDF转为PNG
func (c *Converter) ConvertPDFToPNG(src string, fromPage, toPage int) (pages []Page, err error) {
	return c.convertPDFToPage(src, fromPage, toPage, ".png")
}

func (c *Converter) PDFToPDF(src string) (dst string, err error) {
	dst = c.workspace + "/dst.pdf"
	err = util.CopyFile(src, dst)
	if err != nil {
		c.logger.Error("copy file error", zap.Error(err))
	}
	return
}

// ext 可选值： .png, .svg
func (c *Converter) convertPDFToPage(src string, fromPage, toPage int, ext string) (pages []Page, err error) {
	defer func() {
		if err != nil {
			// 尝试使用 inkscape 转换
			pages, err = c.convertPDFToPageByInkscape(src, fromPage, toPage, ext)
			if err != nil {
				c.logger.Error("convert pdf to page by inkscape", zap.String("cmd", inkscape), zap.Error(err))
			}
		}
	}()

	pageRange := fmt.Sprintf("%d-%d", fromPage, toPage)
	cacheFileFormat := c.workspace + "/%d" + ext
	args := []string{
		"convert",
		"-o",
		cacheFileFormat,
		src,
		pageRange,
	}

	c.logger.Info("convert pdf to page", zap.String("cmd", mutool), zap.Strings("args", args))
	_, err = command.ExecCommand(mutool, args, c.timeout)
	if err != nil {
		c.logger.Error("convert pdf to page", zap.String("cmd", mutool), zap.Strings("args", args), zap.Error(err))
		return
	}

	for i := 0; i <= toPage-fromPage; i++ {
		pagePath := fmt.Sprintf(cacheFileFormat, i+1)
		if _, err = os.Stat(pagePath); err != nil {
			c.logger.Error("convert pdf to page", zap.String("cmd", mutool), zap.Strings("args", args), zap.Error(err))
			break
		}
		pages = append(pages, Page{
			PageNum:  fromPage + i,
			PagePath: pagePath,
		})
	}
	return
}

func (c *Converter) convertPDFToPageByInkscape(src string, fromPage, toPage int, ext string) (pages []Page, err error) {
	cacheFileFormat := c.workspace + "/%d" + ext

	for i := 0; i <= toPage-fromPage; i++ {
		pagePath := fmt.Sprintf(cacheFileFormat, i+1)
		args := []string{
			"-o", pagePath,
			"--pdf-page", fmt.Sprintf("%d", fromPage+i),
			"--pdf-poppler",
		}

		args = append(args, src)

		_, err = command.ExecCommand(inkscape, args, c.timeout)
		if err != nil {
			// 兼容 --pages 参数，替换 --pdf-page 参数
			c.logger.Error("convert pdf to page", zap.String("cmd", inkscape), zap.Strings("args", args), zap.Error(err))
			argsV2 := []string{
				"-o", pagePath,
				"--pages", fmt.Sprintf("%d", fromPage+i),
				"--pdf-poppler",
			}
			argsV2 = append(argsV2, src)
			_, err = command.ExecCommand(inkscape, argsV2, c.timeout)
			if err != nil {
				c.logger.Error("convert pdf to page", zap.String("cmd", inkscape), zap.Strings("args", args), zap.Error(err))
				return
			}
		}

		if _, err = os.Stat(pagePath); err != nil {
			c.logger.Error("convert pdf to page", zap.String("cmd", inkscape), zap.Strings("args", args), zap.Error(err))
			break
		}

		pages = append(pages, Page{
			PageNum:  fromPage + i,
			PagePath: pagePath,
		})
	}
	return
}

func (c *Converter) convertToPDFBySoffice(src string) (dst string, err error) {
	dst = c.workspace + "/" + strings.TrimSuffix(filepath.Base(src), filepath.Ext(src)) + ".pdf"
	args := []string{
		"--headless",
		"--convert-to",
		"pdf",
		"--outdir",
		c.workspace,
	}
	args = append(args, src)
	c.logger.Info("convert to pdf by soffice", zap.String("cmd", soffice), zap.Strings("args", args))
	_, err = command.ExecCommand(soffice, args, c.timeout)
	if err != nil {
		c.logger.Error("convert to pdf by soffice", zap.String("cmd", soffice), zap.Strings("args", args), zap.Error(err))
	}
	return
}

func (c *Converter) convertToPDFByCalibre(src string) (dst string, err error) {
	dst = c.workspace + "/dst.pdf"
	args := []string{
		src,
		dst,
		"--paper-size", "a4",
		// "--pdf-default-font-size", "14",
		"--pdf-page-margin-bottom", "36",
		"--pdf-page-margin-left", "36",
		"--pdf-page-margin-right", "36",
		"--pdf-page-margin-top", "36",
	}
	os.MkdirAll(filepath.Dir(dst), os.ModePerm)
	c.logger.Info("convert to pdf by calibre", zap.String("cmd", ebookConvert), zap.Strings("args", args))
	_, err = command.ExecCommand(ebookConvert, args, c.timeout)
	if err != nil {
		c.logger.Error("convert to pdf by calibre", zap.String("cmd", ebookConvert), zap.Strings("args", args), zap.Error(err))
	}
	return
}

func (c *Converter) CountPDFPages(file string) (pages int, err error) {
	defer func() {
		if err != nil {
			if pages, err = countPDFPagesV2(file); err != nil {
				c.logger.Error("count pdf pages", zap.Error(err))
				return
			}
		}
	}()

	args := []string{
		"show",
		file,
		"pages",
	}
	c.logger.Info("count pdf pages", zap.String("cmd", mutool), zap.Strings("args", args))
	var out string
	out, err = command.ExecCommand(mutool, args, c.timeout)
	if err != nil {
		c.logger.Error("count pdf pages", zap.String("cmd", mutool), zap.Strings("args", args), zap.Error(err))
		return
	}

	lines := strings.Split(out, "\n")
	length := len(lines)
	for i := length - 1; i >= 0; i-- {
		line := strings.TrimSpace(strings.ToLower(lines[i]))
		c.logger.Debug("count pdf pages", zap.String("line", line))
		if strings.HasPrefix(line, "page") {
			pages, _ = strconv.Atoi(strings.TrimSpace(strings.TrimLeft(strings.Split(line, "=")[0], "page")))
			if pages > 0 {
				return
			}
		}
	}
	return
}

func countPDFPagesV2(file string) (pages int, err error) {
	// 优先使用这种PDF分页统计的方式，如果PDF版本比支持，则再使用下一种PDF统计方式。所以这里报错了的话，不返回
	pages, err = getPdfPages(file)
	if err == nil {
		return
	}

	var p []byte
	p, err = os.ReadFile(file)
	if err != nil {
		return
	}

	content := string(p)
	arr := strings.Split(content, "/Pages")
	l := len(arr)
	if l > 0 {
		arr = strings.Split(arr[l-1], "endobj")
		if l = len(arr); l > 0 {
			return len(strings.Split(arr[0], "0 R")) - 1, nil
		}
		return 0, fmt.Errorf(`%v:"endobj"分割时失败`, file)
	}
	return 0, fmt.Errorf(`%v:"/Pages"分割时失败`, file)
}

func getPdfPages(file string) (pages int, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.New(fmt.Sprintf("%v", r))
			return
		}
	}()
	if reader, err := pdf.Open(file); err == nil {
		pages = reader.NumPage()
	}
	return
}

func (c *Converter) ExistMupdf() (err error) {
	_, err = exec.LookPath(mutool)
	return
}

func (c *Converter) ExistSoffice() (err error) {
	_, err = exec.LookPath(soffice)
	return
}

func (c *Converter) ExistCalibre() (err error) {
	_, err = exec.LookPath(ebookConvert)
	return
}

func (c *Converter) ExistSVGO() (err error) {
	_, err = exec.LookPath(svgo)
	return
}

func (c *Converter) CompressSVGBySVGO(svgFolder string) (err error) {
	args := []string{
		"-f",
		svgFolder,
	}
	c.logger.Info("compress svg by svgo", zap.String("cmd", svgo), zap.Strings("args", args))
	var out string
	out, err = command.ExecCommand(svgo, args, c.timeout*10)
	if err != nil {
		c.logger.Error("compress svg by svgo", zap.String("cmd", svgo), zap.Strings("args", args), zap.Error(err))
	}
	c.logger.Info("compress svg by svgo", zap.String("out", out))
	return
}

// CompressSVGByGZIP 将SVG文件压缩为GZIP格式
func (c *Converter) CompressSVGByGZIP(svgFile string) (dst string, err error) {
	var svgBytes []byte
	dst = strings.TrimSuffix(svgFile, filepath.Ext(svgFile)) + ".gzip.svg"
	svgBytes, err = os.ReadFile(svgFile)
	if err != nil {
		c.logger.Error("read svg file", zap.String("svgFile", svgFile), zap.Error(err))
		return
	}

	var replaces = map[string]string{
		`data-text="<"`: `data-text="&lt;"`,
		`data-text=">"`: `data-text="&gt;"`,
	}
	for k, v := range replaces {
		svgBytes = bytes.ReplaceAll(svgBytes, []byte(k), []byte(v))
	}

	var buf bytes.Buffer
	gzw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return "", err
	}
	defer gzw.Close()
	gzw.Write(svgBytes)
	gzw.Flush()
	err = os.WriteFile(dst, buf.Bytes(), os.ModePerm)
	if err != nil {
		c.logger.Error("write svgz file", zap.String("svgzFile", dst), zap.Error(err))
	}
	return
}

func (c *Converter) makeWorkspace() (workspaceDir string) {
	if c.workspace != "" {
		workspaceDir = c.workspace
		return
	}
	uid := uuid.Must(uuid.NewV1()).String()
	c.workspace = filepath.ToSlash(filepath.Join(c.cachePath, time.Now().Format(dirDteFmt), uid))
	return c.workspace
}

func (c *Converter) Clean() (err error) {
	if c.workspace != "" {
		c.logger.Info("clean workspace", zap.String("workspace", c.workspace))
		err = os.RemoveAll(c.workspace)
		if err != nil {
			c.logger.Error("clean workspace", zap.String("workspace", c.workspace), zap.Error(err))
		} else {
			c.logger.Info("clean workspace success", zap.String("workspace", c.workspace))
		}
		c.workspace = ""
	}
	return
}

func ConvertByImageMagick(src, dst string, moreArgs ...string) (err error) {
	args := []string{src}
	args = append(args, moreArgs...)
	args = append(args, dst)
	_, err = command.ExecCommand(imageMagick, args)
	return
}

// 通过Inksacpe进行转换，如将svg转为png
func ConvertByInkscape(src, dst string, moreArgs ...string) (err error) {
	args := []string{
		"-o", dst,
	}
	args = append(args, moreArgs...)
	args = append(args, src)
	_, err = command.ExecCommand(inkscape, args)
	return
}
