package api

//go:generate go run github.com/99designs/gqlgen

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/99designs/gqlgen/graphql"
	"github.com/dstotijn/hetty/pkg/proj"
	"github.com/dstotijn/hetty/pkg/reqlog"
	"github.com/dstotijn/hetty/pkg/scope"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

type Resolver struct {
	RequestLogService *reqlog.Service
	ProjectService    *proj.Service
	ScopeService      *scope.Scope
}

type queryResolver struct{ *Resolver }
type mutationResolver struct{ *Resolver }

func (r *Resolver) Query() QueryResolver       { return &queryResolver{r} }
func (r *Resolver) Mutation() MutationResolver { return &mutationResolver{r} }

func (r *queryResolver) HTTPRequestLogs(ctx context.Context) ([]HTTPRequestLog, error) {
	reqs, err := r.RequestLogService.FindRequests(ctx)
	if err == proj.ErrNoProject {
		return nil, &gqlerror.Error{
			Path:    graphql.GetPath(ctx),
			Message: "No active project.",
			Extensions: map[string]interface{}{
				"code": "no_active_project",
			},
		}
	}
	if err != nil {
		return nil, fmt.Errorf("could not query repository for requests: %v", err)
	}
	logs := make([]HTTPRequestLog, len(reqs))

	for i, req := range reqs {
		req, err := parseRequestLog(req)
		if err != nil {
			return nil, err
		}
		logs[i] = req
	}

	return logs, nil
}

func (r *queryResolver) HTTPRequestLog(ctx context.Context, id int64) (*HTTPRequestLog, error) {
	log, err := r.RequestLogService.FindRequestLogByID(ctx, id)
	if err == reqlog.ErrRequestNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not get request by ID: %v", err)
	}
	req, err := parseRequestLog(log)
	if err != nil {
		return nil, err
	}

	return &req, nil
}

func parseRequestLog(req reqlog.Request) (HTTPRequestLog, error) {
	method := HTTPMethod(req.Request.Method)
	if method != "" && !method.IsValid() {
		return HTTPRequestLog{}, fmt.Errorf("request has invalid method: %v", method)
	}

	log := HTTPRequestLog{
		ID:        req.ID,
		Proto:     req.Request.Proto,
		Method:    method,
		Timestamp: req.Timestamp,
	}

	if req.Request.URL != nil {
		log.URL = req.Request.URL.String()
	}

	if len(req.Body) > 0 {
		reqBody := string(req.Body)
		log.Body = &reqBody
	}

	if req.Request.Header != nil {
		log.Headers = make([]HTTPHeader, 0)
		for key, values := range req.Request.Header {
			for _, value := range values {
				log.Headers = append(log.Headers, HTTPHeader{
					Key:   key,
					Value: value,
				})
			}
		}
	}

	if req.Response != nil {
		log.Response = &HTTPResponseLog{
			RequestID:  req.Response.RequestID,
			Proto:      req.Response.Response.Proto,
			StatusCode: req.Response.Response.StatusCode,
		}
		statusReasonSubs := strings.SplitN(req.Response.Response.Status, " ", 2)
		if len(statusReasonSubs) == 2 {
			log.Response.StatusReason = statusReasonSubs[1]
		}
		if len(req.Response.Body) > 0 {
			resBody := string(req.Response.Body)
			log.Response.Body = &resBody
		}
		if req.Response.Response.Header != nil {
			log.Response.Headers = make([]HTTPHeader, 0)
			for key, values := range req.Response.Response.Header {
				for _, value := range values {
					log.Response.Headers = append(log.Response.Headers, HTTPHeader{
						Key:   key,
						Value: value,
					})
				}
			}
		}
	}

	return log, nil
}

func (r *mutationResolver) OpenProject(ctx context.Context, name string) (*Project, error) {
	p, err := r.ProjectService.Open(ctx, name)
	if err == proj.ErrInvalidName {
		return nil, gqlerror.Errorf("Project name must only contain alphanumeric or space chars.")
	}
	if err != nil {
		return nil, fmt.Errorf("could not open project: %v", err)
	}
	return &Project{
		Name:     p.Name,
		IsActive: p.IsActive,
	}, nil
}

func (r *queryResolver) ActiveProject(ctx context.Context) (*Project, error) {
	p, err := r.ProjectService.ActiveProject()
	if err == proj.ErrNoProject {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not open project: %v", err)
	}

	return &Project{
		Name:     p.Name,
		IsActive: p.IsActive,
	}, nil
}

func (r *queryResolver) Projects(ctx context.Context) ([]Project, error) {
	p, err := r.ProjectService.Projects()
	if err != nil {
		return nil, fmt.Errorf("could not get projects: %v", err)
	}

	projects := make([]Project, len(p))
	for i, proj := range p {
		projects[i] = Project{
			Name:     proj.Name,
			IsActive: proj.IsActive,
		}
	}

	return projects, nil
}

func (r *queryResolver) Scope(ctx context.Context) ([]ScopeRule, error) {
	rules := r.ScopeService.Rules()
	return scopeToScopeRules(rules), nil
}

func regexpToStringPtr(r *regexp.Regexp) *string {
	if r == nil {
		return nil
	}
	s := r.String()
	return &s
}

func (r *mutationResolver) CloseProject(ctx context.Context) (*CloseProjectResult, error) {
	if err := r.ProjectService.Close(); err != nil {
		return nil, fmt.Errorf("could not close project: %v", err)
	}
	return &CloseProjectResult{true}, nil
}

func (r *mutationResolver) DeleteProject(ctx context.Context, name string) (*DeleteProjectResult, error) {
	if err := r.ProjectService.Delete(name); err != nil {
		return nil, fmt.Errorf("could not delete project: %v", err)
	}
	return &DeleteProjectResult{
		Success: true,
	}, nil
}

func (r *mutationResolver) ClearHTTPRequestLog(ctx context.Context) (*ClearHTTPRequestLogResult, error) {
	if err := r.RequestLogService.ClearRequests(ctx); err != nil {
		return nil, fmt.Errorf("could not clear request log: %v", err)
	}
	return &ClearHTTPRequestLogResult{true}, nil
}

func (r *mutationResolver) SetScope(ctx context.Context, input []ScopeRuleInput) ([]ScopeRule, error) {
	rules := make([]scope.Rule, len(input))
	for i, rule := range input {
		u, err := stringPtrToRegexp(rule.URL)
		if err != nil {
			return nil, fmt.Errorf("invalid URL in scope rule: %v", err)
		}
		var headerKey, headerValue *regexp.Regexp
		if rule.Header != nil {
			headerKey, err = stringPtrToRegexp(rule.Header.Key)
			if err != nil {
				return nil, fmt.Errorf("invalid header key in scope rule: %v", err)
			}
			headerValue, err = stringPtrToRegexp(rule.Header.Key)
			if err != nil {
				return nil, fmt.Errorf("invalid header value in scope rule: %v", err)
			}
		}
		body, err := stringPtrToRegexp(rule.Body)
		if err != nil {
			return nil, fmt.Errorf("invalid body in scope rule: %v", err)
		}
		rules[i] = scope.Rule{
			URL: u,
			Header: scope.Header{
				Key:   headerKey,
				Value: headerValue,
			},
			Body: body,
		}
	}

	if err := r.ScopeService.SetRules(ctx, rules); err != nil {
		return nil, fmt.Errorf("could not set scope: %v", err)
	}

	return scopeToScopeRules(rules), nil
}

func (r *queryResolver) HTTPRequestLogFilter(ctx context.Context) (*HTTPRequestLogFilter, error) {
	return findReqFilterToHTTPReqLogFilter(r.RequestLogService.FindReqsFilter), nil
}

func (r *mutationResolver) SetHTTPRequestLogFilter(
	ctx context.Context,
	input *HTTPRequestLogFilterInput,
) (*HTTPRequestLogFilter, error) {
	filter := findRequestsFilterFromInput(input)
	if err := r.RequestLogService.SetRequestLogFilter(ctx, filter); err != nil {
		return nil, fmt.Errorf("could not set request log filter: %v", err)
	}

	empty := reqlog.FindRequestsFilter{}
	if filter == empty {
		return nil, nil
	}
	return findReqFilterToHTTPReqLogFilter(filter), nil
}

func stringPtrToRegexp(s *string) (*regexp.Regexp, error) {
	if s == nil {
		return nil, nil
	}
	return regexp.Compile(*s)
}

func scopeToScopeRules(rules []scope.Rule) []ScopeRule {
	scopeRules := make([]ScopeRule, len(rules))
	for i, rule := range rules {
		scopeRules[i].URL = regexpToStringPtr(rule.URL)
		if rule.Header.Key != nil || rule.Header.Value != nil {
			scopeRules[i].Header = &ScopeHeader{
				Key:   regexpToStringPtr(rule.Header.Key),
				Value: regexpToStringPtr(rule.Header.Value),
			}
		}
		scopeRules[i].Body = regexpToStringPtr(rule.Body)
	}
	return scopeRules
}

func findRequestsFilterFromInput(input *HTTPRequestLogFilterInput) (filter reqlog.FindRequestsFilter) {
	if input == nil {
		return
	}
	if input.OnlyInScope != nil {
		filter.OnlyInScope = *input.OnlyInScope
	}

	return
}

func findReqFilterToHTTPReqLogFilter(findReqFilter reqlog.FindRequestsFilter) *HTTPRequestLogFilter {
	empty := reqlog.FindRequestsFilter{}
	if findReqFilter == empty {
		return nil
	}
	httpReqLogFilter := &HTTPRequestLogFilter{
		OnlyInScope: findReqFilter.OnlyInScope,
	}

	return httpReqLogFilter
}
