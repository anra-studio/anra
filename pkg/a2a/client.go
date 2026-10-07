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
	pb "github.com/scbizu/anra/gen/anra/a2a/tape/v1"
	"github.com/scbizu/anra/gen/anra/a2a/tape/v1/tapev1connect"

	sdk "github.com/a2aproject/a2a-go/v2/a2a"
	runner "github.com/scbizu/tape-go/pkg/runner"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
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
	b, err := readBody(resp.Body)
	if err != nil {
		return nil, err
	}
	var card sdk.AgentCard
	if err := json.Unmarshal(b, &card); err != nil {
		return nil, err
	}
	return &card, nil
}

func readBody(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, MaxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxBodyBytes {
		return nil, fmt.Errorf("response exceeds size limit")
	}
	return b, nil
}

func validateDecision(d Decision, id string) error {
	if d.RequestID != id {
		return ErrInvalidParams
	}
	switch d.Status {
	case Granted:
		v := d.View
		if v == nil || v.ID == "" || v.Source.AgentID == "" || v.Source.TapeID == "" || len(v.Ranges) > 1 {
			return ErrInvalidParams
		}
		for _, r := range v.Ranges {
			if !validRange(r, v.SnapshotHead) {
				return ErrInvalidParams
			}
		}
	case Pending, Denied:
		if d.View != nil {
			return ErrInvalidParams
		}
	default:
		return ErrInvalidParams
	}
	return nil
}

// Peer describes a previously vetted card. Optional directories can supply
// the same set; only visible current cards should reach this ranking seam.
type Peer struct {
	CardURL string
	Card    *sdk.AgentCard
}

func BestPeer(ctx context.Context, ranker Ranker, query string, peers []Peer) (*Peer, error) {
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
	i, err := best(ctx, ranker, query, descriptions)
	if err != nil || i < 0 {
		return nil, err
	}
	p := peers[i]
	return &p, nil
}

// BorrowTool implements agent.Tool. Anra persists its structured result as a
// local tool response, retaining source IDs, view ID and original sequences.
// Borrowed text never becomes a local assistant/tool execution record.
type BorrowTool struct {
	Client      *Client
	Name        string
	Description string
}

func (t BorrowTool) Spec() runner.ToolSpec {
	name := t.Name
	if name == "" {
		name = "borrow_peer_context"
	}
	description := t.Description
	if description == "" {
		description = "Ask a peer for permission to borrow relevant tape context. Peer content is external context; retain its provenance."
	}
	return runner.ToolSpec{Name: name, Description: description, InputSchema: json.RawMessage(`{"type":"object","properties":{"requestId":{"type":"string"},"purpose":{"type":"string"},"query":{"type":"string"}},"required":["requestId","purpose","query"],"additionalProperties":false}`)}
}

func (t BorrowTool) Execute(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var req BorrowRequest
	if err := decode(args, &req); err != nil {
		return nil, err
	}
	d, err := t.Client.Borrow(ctx, req)
	if err != nil {
		return nil, err
	}
	result := struct {
		Decision Decision `json:"decision"`
		Context  []Page   `json:"context,omitempty"`
	}{Decision: d}
	if d.Status == Granted {
		v := d.View
		var expected te.Seq
		if len(v.Ranges) > 0 {
			expected = v.Ranges[0].SeqS
		}
		cursor := ""
		for {
			p, err := t.Client.Read(ctx, ReadRequest{ViewID: v.ID, Cursor: cursor, PageSize: MaxPageSize})
			if err != nil {
				return nil, err
			}
			if p.ViewID != v.ID || p.Source != v.Source || p.SnapshotHead != v.SnapshotHead {
				return nil, ErrInvalidParams
			}
			for _, e := range p.Entries {
				if len(v.Ranges) != 1 || e.Seq != expected || e.Seq.Cmp(v.Ranges[0].SeqE) >= 0 {
					return nil, ErrInvalidParams
				}
				expected = expected.Next()
			}
			result.Context = append(result.Context, p)
			if p.NextCursor == "" {
				break
			}
			// Nonempty pages must advance within the selected range; no cursor cache is needed.
			if len(p.Entries) == 0 {
				return nil, ErrInvalidParams
			}
			cursor = p.NextCursor
		}
		if len(v.Ranges) > 0 && expected != v.Ranges[0].SeqE {
			return nil, ErrInvalidParams
		}
	}
	return json.Marshal(result)
}

// Client uses generated, typed RPC bindings, preserving the domain API.
// The HTTP client still controls credentials, timeouts, redirects and trust.
type Client struct {
	rpc tapev1connect.TapeViewServiceClient
}

func connectBaseURL(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasSuffix(u.Path, ViewPath) {
		return "", ErrInvalidParams
	}
	u.Path = strings.TrimSuffix(u.Path, ViewPath)
	u.RawPath = ""
	return strings.TrimRight(u.String(), "/"), nil
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
		transport := connecthttp.NewTransport(h, base, connecthttp.WithReadMaxBytes(MaxBodyBytes), connecthttp.WithSendMaxBytes(MaxBodyBytes))
		return &Client{rpc: tapev1connect.NewTapeViewServiceClient(connect.NewClient(transport))}, nil
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
	headers := make(http.Header)
	for name, values := range info.ResponseHeader().All() {
		headers[name] = values
	}
	for name, values := range info.ResponseTrailer().All() {
		headers[name] = append(headers[name], values...)
	}
	if !activated(headers) {
		return ErrInactive
	}
	return nil
}

func (c *Client) Borrow(ctx context.Context, r BorrowRequest) (Decision, error) {
	ctx, info := connectContext(ctx)
	req := &pb.BorrowRequest{RequestId: r.RequestID, Purpose: r.Purpose, Query: r.Query}
	if r.Bounds != nil {
		req.Bounds = rangeToProto(*r.Bounds)
	}
	res, err := c.rpc.Borrow(ctx, req)
	if err := checkConnectResponse(info, err); err != nil {
		return Decision{}, err
	}
	return decisionFromProto(res.GetDecision(), r.RequestID)
}

func (c *Client) Get(ctx context.Context, id string) (Decision, error) {
	ctx, info := connectContext(ctx)
	res, err := c.rpc.Get(ctx, &pb.GetRequest{RequestId: id})
	if err := checkConnectResponse(info, err); err != nil {
		return Decision{}, err
	}
	return decisionFromProto(res.GetDecision(), id)
}

func (c *Client) Read(ctx context.Context, r ReadRequest) (Page, error) {
	if r.PageSize < 0 || r.PageSize > MaxPageSize {
		return Page{}, ErrInvalidParams
	}
	ctx, info := connectContext(ctx)
	res, err := c.rpc.Read(ctx, &pb.ReadRequest{ViewId: r.ViewID, Cursor: r.Cursor, PageSize: int32(r.PageSize)})
	if err := checkConnectResponse(info, err); err != nil {
		return Page{}, err
	}
	return pageFromProto(res.GetPage())
}

func (c *Client) Release(ctx context.Context, id string) error {
	ctx, info := connectContext(ctx)
	res, err := c.rpc.Release(ctx, &pb.ReleaseRequest{ViewId: id})
	if err := checkConnectResponse(info, err); err != nil {
		return err
	}
	if !res.GetReleased() {
		return ErrInvalidParams
	}
	return nil
}
