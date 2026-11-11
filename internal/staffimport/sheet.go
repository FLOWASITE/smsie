// Package staffimport đọc danh sách nhân sự từ file .xlsx hoặc .csv (ví dụ file "Xuất Excel" của KeHub → Nhân sự)
// thành các dòng {mã, họ tên, phòng ban, email, điện thoại, đã nghỉ}. Chỉ những cột đó được giữ; mọi cột khác trong
// file (CCCD, tài khoản ngân hàng, lương…) bị bỏ ngay lúc đọc và không bao giờ được lưu hay ghi log.
package staffimport

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

const (
	// MaxFileBytes — trần kích thước file tải lên.
	MaxFileBytes = 5 << 20
	maxRows      = 5000
	maxCols      = 200
	// maxPartBytes — trần dung lượng GIẢI NÉN của mỗi phần trong .xlsx (chống zip bomb).
	maxPartBytes = 32 << 20
)

var (
	ErrUnsupported = errors.New("unsupported file type")
	ErrTooLarge    = errors.New("file too large")
	ErrEmpty       = errors.New("file has no rows")
)

// ReadTable nhận nguyên nội dung file và trả bảng ô chữ. Nhận .xlsx (zip) và .csv (UTF-8, phẩy / chấm phẩy / tab).
func ReadTable(data []byte) ([][]string, error) {
	if len(data) > MaxFileBytes {
		return nil, ErrTooLarge
	}
	switch {
	case bytes.HasPrefix(data, []byte("PK\x03\x04")):
		return readXLSX(data)
	case bytes.HasPrefix(data, []byte{0xD0, 0xCF, 0x11, 0xE0}):
		return nil, fmt.Errorf("%w: file .xls kiểu cũ — hãy lưu lại thành .xlsx hoặc .csv", ErrUnsupported)
	default:
		return readCSV(data)
	}
}

func readCSV(data []byte) ([][]string, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if !isText(data) {
		return nil, fmt.Errorf("%w: không phải .xlsx hay .csv", ErrUnsupported)
	}
	// Đoán dấu ngăn trên vài KB đầu chứ không chỉ dòng đầu: file hay có dòng tên báo cáo phía trên dòng tiêu đề.
	sample := data
	if len(sample) > 4096 {
		sample = sample[:4096]
	}
	delim := ','
	best := bytes.Count(sample, []byte(","))
	for _, d := range []rune{';', '\t'} {
		if n := bytes.Count(sample, []byte(string(d))); n > best {
			delim, best = d, n
		}
	}
	r := csv.NewReader(bytes.NewReader(data))
	r.Comma = delim
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	rows := [][]string{}
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("không đọc được CSV: %w", err)
		}
		// encoding/csv bỏ qua dòng trống; chèn lại dòng rỗng để số dòng báo cho người dùng khớp với file.
		if line, _ := r.FieldPos(0); line > 0 {
			for len(rows) < line-1 && len(rows) < maxRows+20 {
				rows = append(rows, nil)
			}
		}
		if len(rows) >= maxRows+20 {
			return nil, fmt.Errorf("%w: quá %d dòng", ErrTooLarge, maxRows)
		}
		rows = append(rows, rec)
	}
	if len(rows) == 0 {
		return nil, ErrEmpty
	}
	return rows, nil
}

// isText: loại file nhị phân lạ (ảnh, pdf…) trước khi coi nó là CSV.
func isText(data []byte) bool {
	sample := data
	if len(sample) > 2048 {
		sample = sample[:2048]
	}
	return !bytes.ContainsRune(sample, 0)
}

func readXLSX(data []byte) ([][]string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("%w: file .xlsx hỏng", ErrUnsupported)
	}
	files := map[string]*zip.File{}
	sheets := []string{}
	for _, f := range zr.File {
		files[f.Name] = f
		if strings.HasPrefix(f.Name, "xl/worksheets/sheet") && strings.HasSuffix(f.Name, ".xml") {
			sheets = append(sheets, f.Name)
		}
	}
	if len(sheets) == 0 {
		return nil, fmt.Errorf("%w: file .xlsx không có trang tính nào", ErrUnsupported)
	}
	// Trang tính đầu tiên: sheet1.xml nếu có, không thì tên nhỏ nhất (sheet2 trước sheet10).
	sort.Slice(sheets, func(i, j int) bool {
		if len(sheets[i]) != len(sheets[j]) {
			return len(sheets[i]) < len(sheets[j])
		}
		return sheets[i] < sheets[j]
	})

	var shared []string
	if f, ok := files["xl/sharedStrings.xml"]; ok {
		if shared, err = readSharedStrings(f); err != nil {
			return nil, err
		}
	}
	rows, err := readSheet(files[sheets[0]], shared)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrEmpty
	}
	return rows, nil
}

func openPart(f *zip.File) (io.ReadCloser, io.Reader, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: file .xlsx hỏng", ErrUnsupported)
	}
	return rc, io.LimitReader(rc, maxPartBytes), nil
}

// readSharedStrings: mỗi <si> là một chuỗi; chữ nằm trong <t> (trực tiếp hoặc trong các đoạn <r>), bỏ chú âm <rPh>.
func readSharedStrings(f *zip.File) ([]string, error) {
	rc, r, err := openPart(f)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	dec := xml.NewDecoder(r)
	out := []string{}
	var cur strings.Builder
	inSI, inT, phonetic := false, false, 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: file .xlsx hỏng (sharedStrings)", ErrUnsupported)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "si":
				inSI = true
				cur.Reset()
			case "rPh":
				phonetic++
			case "t":
				inT = inSI && phonetic == 0
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "si":
				inSI = false
				out = append(out, cur.String())
			case "rPh":
				phonetic--
			case "t":
				inT = false
			}
		case xml.CharData:
			if inT {
				cur.Write(t)
			}
		}
	}
	return out, nil
}

// colIndex đổi phần chữ của toạ độ ô ("B7" → 1, "AA3" → 26); không đọc được trả -1.
func colIndex(ref string) int {
	n := 0
	seen := false
	for _, ch := range ref {
		switch {
		case ch >= 'A' && ch <= 'Z':
			n = n*26 + int(ch-'A') + 1
			seen = true
		case ch >= 'a' && ch <= 'z':
			n = n*26 + int(ch-'a') + 1
			seen = true
		default:
			if !seen {
				return -1
			}
			return n - 1
		}
	}
	if !seen {
		return -1
	}
	return n - 1
}

func readSheet(f *zip.File, shared []string) ([][]string, error) {
	rc, r, err := openPart(f)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	dec := xml.NewDecoder(r)
	rows := [][]string{}
	var row []string
	var cellType string
	var cellCol int
	var val strings.Builder
	inRow, inCell, inV, inIS, inT := false, false, false, false, false
	nextCol := 0
	bad := fmt.Errorf("%w: file .xlsx hỏng (trang tính)", ErrUnsupported)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, bad
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "row":
				inRow, row, nextCol = true, nil, 0
			case "c":
				if !inRow {
					continue
				}
				inCell, cellType, cellCol = true, "", nextCol
				val.Reset()
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "t":
						cellType = a.Value
					case "r":
						if i := colIndex(a.Value); i >= 0 {
							cellCol = i
						}
					}
				}
			case "v":
				inV = inCell
			case "is":
				inIS = inCell
			case "t":
				inT = inIS
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "v":
				inV = false
			case "t":
				inT = false
			case "is":
				inIS = false
			case "c":
				if !inCell {
					continue
				}
				inCell = false
				if cellCol >= maxCols {
					continue
				}
				text := val.String()
				if cellType == "s" {
					i, err := strconv.Atoi(strings.TrimSpace(text))
					if err != nil || i < 0 || i >= len(shared) {
						return nil, bad
					}
					text = shared[i]
				}
				for len(row) <= cellCol {
					row = append(row, "")
				}
				row[cellCol] = text
				nextCol = cellCol + 1
			case "row":
				inRow = false
				if len(rows) >= maxRows+20 {
					return nil, fmt.Errorf("%w: quá %d dòng", ErrTooLarge, maxRows)
				}
				rows = append(rows, row)
			}
		case xml.CharData:
			if inV || inT {
				val.Write(t)
			}
		}
	}
	return rows, nil
}
