package scan

import "testing"

func TestPickCover_Locked(t *testing.T) {
	got := PickCover(CoverInputs{
		Locked: true,
		Files: []WalkEntry{
			{Location: "Tolkien/Hobbit/hobbit.epub"},
		},
		FolderEntries: []WalkEntry{
			{Location: "Tolkien/Hobbit/cover.jpg"},
		},
		PrimaryFormat: "EPUB",
	})
	if got.Source != CoverLocked {
		t.Errorf("Source=%v want CoverLocked", got.Source)
	}
	if got.Path != "" {
		t.Errorf("Path=%q want empty", got.Path)
	}
}

func TestPickCover_FolderImageBeatsEmbedded(t *testing.T) {
	got := PickCover(CoverInputs{
		Files: []WalkEntry{{Location: "Tolkien/Hobbit/hobbit.epub"}},
		FolderEntries: []WalkEntry{
			{Location: "Tolkien/Hobbit/cover.jpg"},
		},
		PrimaryFormat: "EPUB",
	})
	if got.Source != CoverFolderImage {
		t.Errorf("Source=%v want CoverFolderImage", got.Source)
	}
	if got.Path != "Tolkien/Hobbit/cover.jpg" {
		t.Errorf("Path=%q", got.Path)
	}
}

func TestPickCover_FolderImageExtensionPriority(t *testing.T) {
	got := PickCover(CoverInputs{
		Files: []WalkEntry{{Location: "lib/hobbit.epub"}},
		FolderEntries: []WalkEntry{
			{Location: "lib/cover.webp"},
			{Location: "lib/cover.png"},
			{Location: "lib/cover.jpg"},
		},
		PrimaryFormat: "EPUB",
	})
	if got.Path != "lib/cover.jpg" {
		t.Errorf("Path=%q want lib/cover.jpg (jpg priority)", got.Path)
	}
}

func TestPickCover_FolderImageCaseInsensitive(t *testing.T) {
	got := PickCover(CoverInputs{
		Files: []WalkEntry{{Location: "lib/hobbit.epub"}},
		FolderEntries: []WalkEntry{
			{Location: "lib/Cover.JPG"},
		},
		PrimaryFormat: "EPUB",
	})
	if got.Source != CoverFolderImage {
		t.Errorf("Source=%v want CoverFolderImage", got.Source)
	}
	if got.Path != "lib/Cover.JPG" {
		t.Errorf("Path=%q (preserves on-disk casing)", got.Path)
	}
}

func TestPickCover_NonCoverImagesIgnored(t *testing.T) {
	got := PickCover(CoverInputs{
		Files: []WalkEntry{{Location: "lib/hobbit.epub"}},
		FolderEntries: []WalkEntry{
			{Location: "lib/back-cover.jpg"},
			{Location: "lib/poster.png"},
		},
		PrimaryFormat: "EPUB",
	})
	if got.Source != CoverEmbeddedPrimary {
		t.Errorf("Source=%v want CoverEmbeddedPrimary", got.Source)
	}
	if got.Path != "lib/hobbit.epub" {
		t.Errorf("Path=%q", got.Path)
	}
}

func TestPickCover_PrimaryEmbedded(t *testing.T) {
	got := PickCover(CoverInputs{
		Files: []WalkEntry{
			{Location: "lib/hobbit.epub"},
			{Location: "lib/hobbit.mp3"},
		},
		PrimaryFormat: "EPUB",
	})
	if got.Source != CoverEmbeddedPrimary {
		t.Errorf("Source=%v want CoverEmbeddedPrimary", got.Source)
	}
	if got.Path != "lib/hobbit.epub" {
		t.Errorf("Path=%q want primary file", got.Path)
	}
}

func TestPickCover_CompanionWhenPrimaryMissing(t *testing.T) {
	// PrimaryFormat says EPUB but only an MP3 file exists. Falls
	// through to companion.
	got := PickCover(CoverInputs{
		Files: []WalkEntry{
			{Location: "lib/audiobook.mp3"},
		},
		PrimaryFormat: "EPUB",
	})
	if got.Source != CoverEmbeddedCompanion {
		t.Errorf("Source=%v want CoverEmbeddedCompanion", got.Source)
	}
	if got.Path != "lib/audiobook.mp3" {
		t.Errorf("Path=%q", got.Path)
	}
}

func TestPickCover_SidecarJSONLastResort(t *testing.T) {
	got := PickCover(CoverInputs{
		Files:           nil,
		SidecarHasCover: true,
	})
	if got.Source != CoverSidecarJSON {
		t.Errorf("Source=%v want CoverSidecarJSON", got.Source)
	}
}

func TestPickCover_None(t *testing.T) {
	got := PickCover(CoverInputs{})
	if got.Source != CoverNone {
		t.Errorf("Source=%v want CoverNone", got.Source)
	}
}

func TestPickCover_PrecedenceLockedOverridesAll(t *testing.T) {
	got := PickCover(CoverInputs{
		Locked: true,
		Files: []WalkEntry{
			{Location: "lib/hobbit.epub"},
		},
		FolderEntries: []WalkEntry{
			{Location: "lib/cover.jpg"},
		},
		SidecarHasCover: true,
		PrimaryFormat:   "EPUB",
	})
	if got.Source != CoverLocked {
		t.Errorf("Locked must beat every other source; got %v", got.Source)
	}
}
