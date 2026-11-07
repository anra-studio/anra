package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	rpcpb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"
	"github.com/anra-studio/anra/gen/anra/a2a/tape/v1/tapev1connect"
	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"

	sdk "github.com/a2aproject/a2a-go/v2/a2a"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"google.golang.org/protobuf/proto"
)

func Discover(ctx context.Context, h *http.Client, cardURL string) (*sdk.AgentCard, error) {
	if h == nil {
		return nil, ErrInvalidParams
	}
	req, err := http.NewRequestWithContext(ctx, "GET", cardURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("card discovery: HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var card sdk.AgentCard
	if err := json.Unmarshal(b, &card); err != nil {
		return nil, err
	}
	return &card, nil
}

// Peer describes a previously vetted card. Optional directories can supply
// the same set; only visible current cards should reach this ranking seam.
type Peer struct {
	CardURL string

	Card *sdk.AgentCard
}

// RankPeers returns the peers the ranker considers worth visiting for the
// query, best first. The ranker decides how many (top-N or threshold); an
// empty result means no match.
func RankPeers(ctx context.Context, ranker Ranker, query string, peers []Peer) ([]Peer, error) {
	descriptions := make([]string, len(peers))
	for i, p := range peers {
		if p.Card == nil {
			return nil, ErrInvalidParams
		}
		descriptions[i] = p.Card.Name + "\n" + p.Card.Description
		for _, skill := range p.Card.Skills {
			descriptions[i] += "\n" + skill.Name + ": " + skill.Description
		}
	}
	order, err := rank(ctx, ranker, query, descriptions)
	if err != nil {
		return nil, err
	}
	ranked := make([]Peer, len(order))
	for n, i := range order {
		ranked[n] = peers[i]
	}
	return ranked, nil
}

// Client uses generated protobuf messages directly.
// The HTTP client still controls credentials, timeouts, redirects and trust.
type Client struct {
	rpc tapev1connect.TapeViewServiceClient

	viewVersion bool
}

// connectBaseURL returns the Connect base URL for an advertised endpoint. The
// endpoint is the full service URL, so it must be absolute and end in ViewPath;
// the generated client appends the service and method itself.
func connectBaseURL(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	base, ok := strings.CutSuffix(endpoint, ViewPath)
	if !ok || !u.IsAbs() || u.Host == "" {
		return "", ErrInvalidParams
	}
	return base, nil
}

func NewClient(h *http.Client, card *sdk.AgentCard) (*Client, error) {
	if h == nil || card == nil {
		return nil, ErrInvalidParams
	}
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI != ExtensionURI {
			continue
		}
		value, exists := ext.Params["endpoint"]
		if !exists {
			return nil, ErrInactive
		}
		endpoint, ok := value.(string)
		if !ok {
			return nil, ErrInvalidParams
		}
		base, err := connectBaseURL(endpoint)
		if err != nil {
			return nil, err
		}
		transport := connecthttp.NewTransport(h, base, connecthttp.WithReadMaxBytes(0), connecthttp.WithSendMaxBytes(0))
		version, _ := json.Marshal(ext.Params["viewBorrowVersion"])

		return &Client{
			rpc:         tapev1connect.NewTapeViewServiceClient(connect.NewClient(transport)),
			viewVersion: string(version) == "1",
		}, nil

	}
	return nil, ErrInactive
}

func connectContext(ctx context.Context) (context.Context, *connect.CallInfo) {
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set(ExtensionHeader, ExtensionURI)
	return ctx, info
}

func checkConnectResponse(info *connect.CallInfo, err error) error {
	if err != nil {
		return fromConnectError(err)
	}
	if !extensionActive(info.ResponseHeader()) {
		return ErrInactive
	}
	return nil
}

func (c *Client) Borrow(ctx context.Context, r *pb.BorrowRequest) (*pb.Decision, error) {
	if err := validateMessage(r); err != nil {
		return nil, err
	}
	if r.View != nil && !c.viewVersion {
		return nil, ErrInactive
	}
	ctx, info := connectContext(ctx)
	res, err := c.rpc.Borrow(ctx, r)
	if err := checkConnectResponse(info, err); err != nil {
		return nil, err
	}
	d := res.GetDecision()
	if err := validateMessage(d); err != nil {
		return nil, err
	}

	if d.RequestId != r.RequestId || (d.Status == Granted && r.View != nil && !viewMatches(r.View, d.Lease)) {
		return nil, ErrInvalidParams
	}
	return d, nil
}

// BorrowContext requests a grant and reads its complete permitted view. Denied
// and pending decisions return without reads. Snapshot validation and ordered
// pagination are shared with every other protocol caller through Borrow/ReadView.
func (c *Client) BorrowContext(ctx context.Context, req *pb.BorrowRequest) (*pb.Decision, []*pb.Quote, error) {
	decision, err := c.Borrow(ctx, req)
	if err != nil {
		return nil, nil, err
	}
	if decision.Status != Granted {
		return decision, nil, nil
	}

	pages, err := c.ReadView(ctx, decision.Lease)
	if err != nil {
		return nil, nil, err
	}
	return decision, pages, nil
}

func (c *Client) Get(ctx context.Context, id string) (*pb.Decision, error) {
	ctx, info := connectContext(ctx)

	res, err := c.rpc.Get(
		ctx,
		&rpcpb.GetRequest{
			RequestId: id,
		},
	)

	if err := checkConnectResponse(info, err); err != nil {
		return nil, err
	}
	d := res.GetDecision()
	return d, validateMessage(d)
}

func (c *Client) Read(ctx context.Context, r *rpcpb.ReadRequest) (*rpcpb.ReadResponse, error) {
	if err := validateMessage(r); err != nil {
		return nil, err
	}
	ctx, info := connectContext(ctx)
	res, err := c.rpc.Read(ctx, r)
	if err := checkConnectResponse(info, err); err != nil {
		return nil, err
	}

	return res, validateMessage(res)
}

// ReadView reads every page of a granted view. A view that fits in one page
// costs one Read call. Every Quote must match the granted caller, lease and Ref;
// together the pages must cover the granted scopes exactly once, in order.
func (c *Client) ReadView(ctx context.Context, lease *pb.Lease) ([]*pb.Quote, error) {
	if err := validateMessage(lease); err != nil {
		return nil, err
	}

	var pages []*pb.Quote

	var cursor string

	scopeIndex := 0

	expected := te.MustParseSeq(lease.Ref.Scopes[0].SeqS)

	for {

		page, err := c.Read(
			ctx,
			&rpcpb.ReadRequest{
				LeaseId:  lease.Id,
				Cursor:   cursor,
				PageSize: MaxPageSize,
			},
		)
		if err != nil {
			return nil, err
		}
		p := page.Quote

		if p.Provenance.LeaseId != lease.Id || p.Provenance.Caller != lease.Caller || !proto.Equal(p.Provenance.Ref, lease.Ref) {
			return nil, ErrInvalidParams
		}
		for _, e := range p.Entries {

			if scopeIndex >= len(lease.Ref.Scopes) || e.Seq != expected.String() {
				return nil, ErrInvalidParams
			}
			expected = expected.Next()

			if expected.String() == lease.Ref.Scopes[scopeIndex].SeqE {
				scopeIndex++
				if scopeIndex < len(lease.Ref.Scopes) {
					expected = te.MustParseSeq(lease.Ref.Scopes[scopeIndex].SeqS)
				}
			}

		}
		pages = append(pages, p)
		if page.NextCursor == "" {
			if scopeIndex != len(lease.Ref.Scopes) {
				return nil, ErrInvalidParams
			}
			return pages, nil
		}

		if len(p.Entries) == 0 || scopeIndex == len(lease.Ref.Scopes) {
			return nil, ErrInvalidParams
		}
		cursor = page.NextCursor
	}
}

func (c *Client) Release(ctx context.Context, id string) error {
	ctx, info := connectContext(ctx)

	res, err := c.rpc.Release(
		ctx,
		&rpcpb.ReleaseRequest{
			LeaseId: id,
		},
	)

	if err := checkConnectResponse(info, err); err != nil {
		return err
	}
	if !res.GetReleased() {
		return ErrInvalidParams
	}
	return nil
}
