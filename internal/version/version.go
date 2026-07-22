package version

// Release is replaced by GoReleaser. Source builds deliberately identify
// themselves as development builds so the inspector never suggests a release
// download that may not match the running service.
var Release = "development"
