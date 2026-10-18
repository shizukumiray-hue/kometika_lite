package terabox

import (
	"testing"
)

func TestTeraboxMayumiComponents(t *testing.T) {
	randStr, timeStr := teraboxGenerateRand()
	if len(randStr) != 16 {
		t.Errorf("teraboxGenerateRand randStr length = %d, expected 16", len(randStr))
	}
	if timeStr == "" {
		t.Errorf("teraboxGenerateRand timeStr is empty")
	}

	testURL := "https://terabox.com/s/1a2b3c4d5e"
	surl := teraboxExtractSurl(testURL, nil)
	if surl != "1a2b3c4d5e" {
		t.Errorf("teraboxExtractSurl = %s, expected 1a2b3c4d5e", surl)
	}

	sizeStr := teraboxFormatFileSize(1048576)
	if sizeStr != "1.00 MB" {
		t.Errorf("teraboxFormatFileSize = %s, expected 1.00 MB", sizeStr)
	}
}
