package bones

// ErrMapper maps errors to other things, often other errors
//
// At the edge of the application they usually map to a structure
// that you're happy publishing, and may have the ability to log
// privately what's going on for debug
//
// Internally, at your application's boundaries (db <-> service <-> etc.)
// it can be used to map/wrap errors from internal dependencies so that
// layers using a package don't have to know/deal with errors from every
// undocumented dependency you've got.
//
// e.g. an API layer can just map from service layers to public facing messages
// without having to care about or expose internal database errors.
type ErrMapper[T any] func(error) T
