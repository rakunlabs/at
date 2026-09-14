package server

import mfolder "github.com/rakunlabs/ada/handler/folder"

func uiFolderConfig(basePath string) *mfolder.Config {
	return &mfolder.Config{
		BasePath:       basePath,
		Index:          true,
		StripIndexName: true,
		SPA:            true,
		PrefixPath:     basePath,
		CacheRegex: []*mfolder.RegexCacheStore{
			{Regex: `^index\.html$`, CacheControl: "no-store"},
			// These stable URLs can change between releases. Revalidate instead
			// of letting a browser/proxy retain an old manifest, worker or logo.
			// The folder handler matches base names, not full request paths.
			{Regex: `^(manifest\.webmanifest|workspace-media\.js|offline\.html|favicon(?:-\d+x\d+)?\.(?:svg|png|ico))$`, CacheControl: "no-cache"},
		},
	}
}
