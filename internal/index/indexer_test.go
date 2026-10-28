package index_test

import (
	"strings"
	"testing"

	"github.com/alanhakhyeonsong/grimoire/internal/index"
)

func TestUnclassifiedNoticeEmptyWhenNothingMissing(t *testing.T) {
	if got := index.UnclassifiedNotice(0, nil); got != "" {
		t.Errorf("누락 0건이면 빈 문자열 기대, got %q", got)
	}
}

func TestUnclassifiedNoticeListsDirsAndRemedy(t *testing.T) {
	got := index.UnclassifiedNotice(3, []string{"projects", "skills"})
	for _, want := range []string{"3건", "projects, skills", "taxonomy.directories", "boundary.locked_dirs"} {
		if !strings.Contains(got, want) {
			t.Errorf("안내에 %q 가 없음: %s", want, got)
		}
	}
}

// 루트 직속 파일은 디렉토리 후보가 없다. 빈 목록 대신 원인을 밝혀야 한다.
func TestUnclassifiedNoticeRootFilesWithoutDirs(t *testing.T) {
	got := index.UnclassifiedNotice(1, nil)
	if !strings.Contains(got, "KB 루트 직속 파일") {
		t.Errorf("디렉토리 후보가 없을 때 루트 직속 안내 기대: %s", got)
	}
}
