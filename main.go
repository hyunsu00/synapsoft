package main

import (
	"fmt"
	"html/template"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/disintegration/imaging"
	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/references"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"
)

type TextItem struct {
	Text   string
	Left   float64
	Top    float64
	Width  float64
	Height float64
}

type PageData struct {
	PageNum    int
	Image      string
	Texts      []TextItem
	Width      float64
	Height     float64
	RealWidth  float64 // PDF 페이지의 실제 너비
	RealHeight float64 // PDF 페이지의 실제 높이
}

func main() {
	pdfPath := "compressed.tracemonkey-pldi-09.pdf"
	outputDir := "output"

	// 1️⃣ WebAssembly PDFium Pool 초기화
	pool, err := webassembly.Init(webassembly.Config{
		MinIdle:  1,
		MaxIdle:  2,
		MaxTotal: 2,
	})
	if err != nil {
		log.Fatalf("pdfium webassembly init error: %v", err)
	}
	defer pool.Close()

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		log.Fatal(err)
	}

	// 2️⃣ Pool에서 PDFium 인스턴스 가져오기 (타임아웃 설정)
	pdfiumInstance, err := pool.GetInstance(time.Second * 30)
	if err != nil {
		log.Fatalf("get pdfium instance error: %v", err)
	}
	defer func() {
		if err := pool.Close(); err != nil {
			log.Printf("pool close error: %v", err)
		}
	}()

	// 3️⃣ PDF 파일 읽기
	pdfData, err := os.ReadFile(pdfPath)
	if err != nil {
		log.Fatalf("read pdf file error: %v", err)
	}

	// 4️⃣ PDF 데이터를 ReadSeeker로 변환
	pdfReader := strings.NewReader(string(pdfData))

	// 5️⃣ 문서 열기
	doc, err := pdfiumInstance.OpenDocument(&requests.OpenDocument{
		FileReader:     pdfReader,
		FileReaderSize: int64(len(pdfData)),
	})
	if err != nil {
		log.Fatalf("Failed to open pdf: %v", err)
	}

	// 6️⃣ 페이지 수 확인
	pages, err := pdfiumInstance.FPDF_GetPageCount(&requests.FPDF_GetPageCount{
		Document: doc.Document,
	})
	if err != nil {
		log.Fatalf("Get page count error: %v", err)
	}

	log.Printf("PDF page count: %d", pages.PageCount)

	// 7️⃣ 페이지 단위 처리
	for i := 0; i < pages.PageCount; i++ {
		err := processPage(pdfiumInstance, doc.Document, i, outputDir)
		if err != nil {
			log.Printf("Process page %d error: %v", i+1, err)
		}
	}

	log.Println("✅ 완료: output 폴더에 HTML 생성됨.")
}

func processPage(instance pdfium.Pdfium, document references.FPDF_DOCUMENT, pageIndex int, outputDir string) error {
	// 페이지 로드
	pageRes, err := instance.FPDF_LoadPage(&requests.FPDF_LoadPage{
		Document: document,
		Index:    pageIndex,
	})
	if err != nil {
		return fmt.Errorf("load page error: %w", err)
	}
	defer func() {
		_, closeErr := instance.FPDF_ClosePage(&requests.FPDF_ClosePage{Page: pageRes.Page})
		if closeErr != nil {
			log.Printf("close page error: %v", closeErr)
		}
	}()

	// 페이지 크기 가져오기
	size, err := instance.FPDF_GetPageSizeByIndex(&requests.FPDF_GetPageSizeByIndex{
		Document: document,
		Index:    pageIndex,
	})
	if err != nil {
		return fmt.Errorf("get page size error: %w", err)
	}

	width := size.Width
	height := size.Height

	// 이미지 렌더링 - 웹 브라우저 표준 해상도로 생성 (96 DPI)
	imgRes, err := instance.RenderPageInDPI(&requests.RenderPageInDPI{
		Page: requests.Page{
			ByReference: &pageRes.Page,
		},
		DPI: 96, // 웹 브라우저 표준 DPI (96 DPI = 72 DPI * 1.3333333)
	})
	if err != nil {
		return fmt.Errorf("render page error: %w", err)
	}

	// 이미지 저장
	imgFile := filepath.Join(outputDir, fmt.Sprintf("page%d.png", pageIndex+1))
	if err := imaging.Save(imgRes.Result.Image, imgFile); err != nil {
		return fmt.Errorf("save image error: %w", err)
	}

	// DPI 변환 비율 계산 (96 DPI / 72 DPI = 1.3333333)
	dpiScale := 96.0 / 72.0

	// 텍스트 추출
	texts, err := extractTexts(instance, pageRes.Page, height, dpiScale)
	if err != nil {
		log.Printf("Extract texts error: %v", err)
		texts = []TextItem{} // 빈 배열로 계속 진행
	}

	// 페이지 데이터 구성 - 이미지 크기에 맞춰 스케일링
	pageData := PageData{
		PageNum:    pageIndex + 1,
		Image:      filepath.Base(imgFile),
		Texts:      texts,
		Width:      width * dpiScale,  // 96 DPI로 스케일링된 너비
		Height:     height * dpiScale, // 96 DPI로 스케일링된 높이
		RealWidth:  width,             // PDF 페이지의 실제 너비
		RealHeight: height,            // PDF 페이지의 실제 높이
	}

	// HTML 파일 생성
	htmlFile := filepath.Join(outputDir, fmt.Sprintf("page%d.html", pageIndex+1))
	return renderHTML(pageData, htmlFile)
}

func extractTexts(instance pdfium.Pdfium, page references.FPDF_PAGE, pageHeight float64, dpiScale float64) ([]TextItem, error) {
	// 텍스트 페이지 로드 - references.FPDF_PAGE를 requests.Page로 변환
	textPage, err := instance.FPDFText_LoadPage(&requests.FPDFText_LoadPage{
		Page: requests.Page{
			ByReference: &page,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("load text page error: %w", err)
	}
	defer func() {
		_, closeErr := instance.FPDFText_ClosePage(&requests.FPDFText_ClosePage{TextPage: textPage.TextPage})
		if closeErr != nil {
			log.Printf("close text page error: %v", closeErr)
		}
	}()

	// 문자 수 확인
	charCount, err := instance.FPDFText_CountChars(&requests.FPDFText_CountChars{
		TextPage: textPage.TextPage,
	})
	if err != nil {
		return nil, fmt.Errorf("count chars error: %w", err)
	}

	var texts []TextItem

	for j := 0; j < charCount.Count; j++ {
		// 문자별 좌표 추출
		rect, err := instance.FPDFText_GetCharBox(&requests.FPDFText_GetCharBox{
			TextPage: textPage.TextPage,
			Index:    j,
		})
		if err != nil {
			continue
		}

		// 개별 문자 가져오기
		charRes, err := instance.FPDFText_GetText(&requests.FPDFText_GetText{
			TextPage:   textPage.TextPage,
			StartIndex: j,
			Count:      1,
		})
		if err != nil {
			continue
		}

		char := charRes.Text

		// 모든 문자 처리 (공백 포함)
		if char != "\n" && char != "\r" && char != "\t" && char != "" {
			// PDF 좌표계를 CSS 좌표계로 변환 (DPI 스케일링 적용)
			// PDF는 왼쪽 하단이 원점, CSS는 왼쪽 상단이 원점
			// 베이스라인 기준으로 정확한 위치 계산 후 DPI 스케일링 적용
			left := rect.Left * dpiScale
			top := (pageHeight - rect.Bottom) * dpiScale // Bottom을 사용해 베이스라인 맞춤
			width := (rect.Right - rect.Left) * dpiScale
			height := (rect.Top - rect.Bottom) * dpiScale

			texts = append(texts, TextItem{
				Text:   char,
				Left:   left,
				Top:    top - height, // 텍스트 상단으로 보정
				Width:  width,
				Height: height,
			})
		}
	}

	return texts, nil
}

// HTML 템플릿 렌더링
func renderHTML(data PageData, outputFile string) error {
	tmpl, err := template.ParseFiles("templates/page.html")
	if err != nil {
		return fmt.Errorf("parse template error: %w", err)
	}

	f, err := os.Create(outputFile)
	if err != nil {
		return fmt.Errorf("create file error: %w", err)
	}
	defer f.Close()

	return tmpl.Execute(f, data)
}
