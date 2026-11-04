package syncbundlebuild

import "context"

// ProjectIgnorePatterns freezes the ordinary execution exclusions into each
// requested source view. They remain explicit when packaging the union.
func ProjectIgnorePatterns(ctx context.Context, cwd string) ([]string, error) {
	root, err := repositoryRoot(ctx, cwd)
	if err != nil {
		return nil, err
	}
	return readProjectIgnore(root)
}
