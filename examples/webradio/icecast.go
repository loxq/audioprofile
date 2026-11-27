package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// icecastTarget est ce qu'on sait d'un point de montage Icecast à partir de l'URL de sortie
// (icecast://source:mot-de-passe@hote:8000/montage).
type icecastTarget struct {
	baseURL string // http://hote:8000
	mount   string // /montage
}

// parseIcecastURL reconnaît une URL icecast:// et en tire le serveur et le point de montage. ok est faux pour toute
// autre sortie (un fichier, par exemple) : il n'y a alors rien à annoncer.
func parseIcecastURL(output string) (t icecastTarget, ok bool) {
	u, err := url.Parse(output)
	if err != nil || (u.Scheme != "icecast" && u.Scheme != "icecast+tls") || u.Host == "" || u.Path == "" {
		return t, false
	}
	scheme := "http"
	if u.Scheme == "icecast+tls" {
		scheme = "https"
	}
	return icecastTarget{baseURL: scheme + "://" + u.Host, mount: u.Path}, true
}

// adminClient met à jour le titre affiché par Icecast, par l'interface d'administration.
type adminClient struct {
	target   icecastTarget
	user     string
	password string
	http     *http.Client
}

func newAdminClient(t icecastTarget, user, password string) *adminClient {
	return &adminClient{target: t, user: user, password: password, http: &http.Client{Timeout: 5 * time.Second}}
}

// UpdateSong annonce le titre en cours. Le son ne passe pas par là : un échec n'interrompt jamais la diffusion.
func (a *adminClient) UpdateSong(ctx context.Context, title string) error {
	q := url.Values{"mount": {a.target.mount}, "mode": {"updinfo"}, "song": {title}}
	req, err := http.NewRequestWithContext(ctx, "GET", a.target.baseURL+"/admin/metadata?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(a.user, a.password)
	resp, err := a.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("icecast a répondu %s", resp.Status)
	}
	if !strings.Contains(string(body), "<return>1</return>") { // Icecast répond 200 avec <return>0</return> en cas de refus
		return fmt.Errorf("icecast a refusé la mise à jour du titre : %s", strings.TrimSpace(string(body)))
	}
	return nil
}
