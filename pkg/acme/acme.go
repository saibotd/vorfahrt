package acme

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

type AcmeJSON map[string]AcmeResolver

type AcmeResolver struct {
	Certificates []AcmeCertificate `json:"Certificates"`
}

type AcmeCertificate struct {
	Domain struct {
		Main string   `json:"main"`
		Sans []string `json:"sans"`
	} `json:"domain"`
	Certificate string `json:"certificate"`
	Key         string `json:"key"`
}

type Provider struct {
	mu      sync.RWMutex
	config  *tls.Config
	domains []string
	path    string
}

func NewProvider(path string) *Provider {
	p := &Provider{path: path}
	p.Reload()
	go p.watch()
	return p
}

func (p *Provider) watch() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Println("Failed to start watcher:", err)
		return
	}
	defer watcher.Close()

	_ = watcher.Add(filepath.Dir(p.path))
	_ = watcher.Add(p.path)

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			isTargetFile := filepath.Clean(event.Name) == filepath.Clean(p.path) || filepath.Base(event.Name) == filepath.Base(p.path)
			if isTargetFile && (event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create) {
				log.Println("acme.json modified, reloading...")
				if err := p.Reload(); err != nil {
					log.Println("Error reloading acme.json:", err)
				}
			}
			if isTargetFile && (event.Op&fsnotify.Remove == fsnotify.Remove || event.Op&fsnotify.Rename == fsnotify.Rename) {
				_ = watcher.Add(p.path)
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Println("Watcher error:", err)
		}
	}
}

func (p *Provider) Reload() error {
	data, err := os.ReadFile(p.path)
	if err != nil {
		return err
	}

	var acmeData AcmeJSON
	if err := json.Unmarshal(data, &acmeData); err != nil {
		return err
	}

	cfg := &tls.Config{
		Certificates: []tls.Certificate{},
	}
	var domains []string
	domainMap := make(map[string]bool)

	for _, resolver := range acmeData {
		for _, cert := range resolver.Certificates {
			certData, err := base64.StdEncoding.DecodeString(cert.Certificate)
			if err != nil {
				continue
			}
			keyData, err := base64.StdEncoding.DecodeString(cert.Key)
			if err != nil {
				continue
			}

			tlsCert, err := tls.X509KeyPair(certData, keyData)
			if err != nil {
				continue
			}
			cfg.Certificates = append(cfg.Certificates, tlsCert)
			
			if cert.Domain.Main != "" && !domainMap[cert.Domain.Main] {
				domainMap[cert.Domain.Main] = true
				domains = append(domains, cert.Domain.Main)
			}
			for _, san := range cert.Domain.Sans {
				if san != "" && !domainMap[san] {
					domainMap[san] = true
					domains = append(domains, san)
				}
			}
		}
	}

	if len(cfg.Certificates) == 0 {
		return fmt.Errorf("no certificates found in %s", p.path)
	}

	p.mu.Lock()
	p.config = cfg
	p.domains = domains
	p.mu.Unlock()
	log.Printf("Loaded %d certificates from %s", len(cfg.Certificates), p.path)
	return nil
}

func (p *Provider) Domains() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	
	domains := make([]string, len(p.domains))
	copy(domains, p.domains)
	return domains
}

func (p *Provider) TLSConfig() *tls.Config {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.config == nil {
		return nil
	}
	// Return a clone that points to the same certificates
	return &tls.Config{
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			p.mu.RLock()
			defer p.mu.RUnlock()
			if p.config == nil || len(p.config.Certificates) == 0 {
				return nil, fmt.Errorf("no certificates available")
			}
			for _, c := range p.config.Certificates {
				if err := hello.SupportsCertificate(&c); err == nil {
					return &c, nil
				}
			}
			return &p.config.Certificates[0], nil
		},
	}
}
