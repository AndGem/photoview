package resolvers

// This file will be automatically regenerated based on the schema, any resolver implementations
// will be copied through when generating and any unknown code will be moved to the end.
// Code generated by github.com/99designs/gqlgen version v0.17.68

import (
	"context"
	"time"

	"github.com/photoview/photoview/api/graphql/auth"
	"github.com/photoview/photoview/api/graphql/models"
	"github.com/photoview/photoview/api/graphql/models/actions"
)

// MyTimeline is the resolver for the myTimeline field.
func (r *queryResolver) MyTimeline(ctx context.Context, paginate *models.Pagination, onlyFavorites *bool, fromDate *time.Time) ([]*models.Media, error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, auth.ErrUnauthorized
	}

	return actions.MyTimeline(r.DB(ctx), user, paginate, onlyFavorites, fromDate)
}
