package docscheck

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/loxq/audioprofile"
	"github.com/loxq/audioprofile/config"
)

const (
	root   = "../.."
	module = "github.com/loxq/audioprofile"
)

// ---------------------------------------------------------------- lecture des fichiers Markdown

type block struct {
	file string // chemin relatif à la racine
	line int    // ligne de la clôture ouvrante (1 = première ligne du fichier)
	lang string
	body string
	skip bool
}

type mdFile struct {
	path   string // relatif à la racine, avec des /
	lines  []string
	blocks []block
	// anchors : titres (hors blocs de code), sous la forme des ancres de GitHub
	anchors map[string]bool
}

// slug donne l'ancre GitHub d'un titre : minuscules, sans ponctuation, espaces en tirets.
var notSlug = regexp.MustCompile(`[^\p{L}\p{N}_\- ]`)

func slug(title string) string {
	return strings.ReplaceAll(notSlug.ReplaceAllString(strings.ToLower(strings.TrimSpace(title)), ""), " ", "-")
}

func loadMarkdown(t *testing.T) []*mdFile {
	t.Helper()
	paths := []string{"README.md"}
	docs, err := filepath.Glob(filepath.Join(root, "docs", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(docs)
	for _, d := range docs {
		paths = append(paths, "docs/"+filepath.Base(d))
	}
	examples, err := filepath.Glob(filepath.Join(root, "examples", "*", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(examples)
	for _, e := range examples {
		rel, _ := filepath.Rel(root, e)
		paths = append(paths, filepath.ToSlash(rel))
	}
	var out []*mdFile
	for _, p := range paths {
		data, err := os.ReadFile(filepath.Join(root, p))
		if err != nil {
			t.Fatal(err)
		}
		f := &mdFile{path: p, lines: strings.Split(string(data), "\n"), anchors: map[string]bool{}}
		seen := map[string]int{}
		var cur *block
		skipNext := false
		for i, l := range f.lines {
			if strings.HasPrefix(l, "```") {
				if cur == nil {
					cur = &block{file: p, line: i + 1, lang: strings.TrimSpace(strings.TrimPrefix(l, "```")), skip: skipNext}
				} else {
					f.blocks = append(f.blocks, *cur)
					cur = nil
				}
				skipNext = false
				continue
			}
			if cur != nil {
				cur.body += l + "\n"
				continue
			}
			skipNext = strings.TrimSpace(l) == "<!-- doccheck:skip -->"
			if m := regexp.MustCompile(`^#{1,6} (.*)$`).FindStringSubmatch(l); m != nil {
				s := slug(m[1])
				if n := seen[s]; n > 0 { // GitHub numérote les titres en double
					f.anchors[fmt.Sprintf("%s-%d", s, n)] = true
				} else {
					f.anchors[s] = true
				}
				seen[s]++
			}
		}
		if cur != nil {
			t.Fatalf("%s:%d : bloc de code jamais refermé", p, cur.line)
		}
		out = append(out, f)
	}
	return out
}

// ---------------------------------------------------------------- liens

var link = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)\)`)

func TestLinks(t *testing.T) {
	files := loadMarkdown(t)
	byPath := map[string]*mdFile{}
	for _, f := range files {
		byPath[f.path] = f
	}
	checked := 0
	for _, f := range files {
		inFence := false
		for i, l := range f.lines {
			if strings.HasPrefix(l, "```") {
				inFence = !inFence
			}
			if inFence {
				continue
			}
			for _, m := range link.FindAllStringSubmatch(l, -1) {
				target := m[1]
				if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
					continue
				}
				checked++
				pathPart, anchor, _ := strings.Cut(target, "#")
				dest := f.path
				if pathPart != "" {
					dest = filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(f.path), pathPart)))
				}
				if strings.HasPrefix(dest, "../") {
					t.Errorf("%s:%d : le lien %q sort du dépôt", f.path, i+1, target)
					continue
				}
				if _, err := os.Stat(filepath.Join(root, dest)); err != nil {
					t.Errorf("%s:%d : le lien %q pointe vers un fichier absent (%s)", f.path, i+1, target, dest)
					continue
				}
				if anchor == "" {
					continue
				}
				md, ok := byPath[dest]
				if !ok {
					continue // ancre dans autre chose qu'un de nos Markdown (ligne de code, par exemple)
				}
				if !md.anchors[anchor] {
					t.Errorf("%s:%d : le lien %q pointe vers une ancre absente de %s", f.path, i+1, target, dest)
				}
			}
		}
	}
	if checked < 50 {
		t.Errorf("seulement %d liens vérifiés : l'extraction ne marche plus ?", checked)
	}
}

// ---------------------------------------------------------------- blocs Go

// known donne, pour les paquets qu'un fragment peut citer sans importer, le chemin d'import.
var known = map[string]string{
	"audioprofile": module,
	"config":       module + "/config",
	"fx":           module + "/fx",
	"live":         module + "/live",
	"panel":        module + "/panel",
	"io":           "io",
	"os":           "os",
	"log":          "log",
	"fmt":          "fmt",
	"time":         "time",
	"bytes":        "bytes",
	"strings":      "strings",
	"http":         "net/http",
}

type checker struct {
	fset *token.FileSet
	imp  types.Importer
}

func newChecker(t *testing.T) *checker {
	t.Helper()
	cgo := build.Default.CgoEnabled
	build.Default.CgoEnabled = false // l'importeur de sources n'a pas besoin de cgo, et il serait lent avec
	t.Cleanup(func() { build.Default.CgoEnabled = cgo })
	fset := token.NewFileSet()
	return &checker{fset: fset, imp: importer.ForCompiler(fset, "source", nil)}
}

// tolerated : erreurs ignorées dans un fragment, qui n'est pas un programme (variables et imports non utilisés).
func tolerated(msg string) bool {
	return strings.Contains(msg, "declared and not used") || strings.Contains(msg, "imported and not used")
}

// check vérifie qu'un fichier source compile ; lenient tolère les variables et imports inutilisés.
// Les numéros de ligne des erreurs sont décalés de offset.
func (c *checker) check(src string, lenient bool, offset int) []string {
	f, err := parser.ParseFile(c.fset, "snippet.go", src, 0)
	if err != nil {
		return []string{shiftLines(err.Error(), offset)}
	}
	var errs []string
	conf := types.Config{
		Importer: c.imp,
		Error: func(err error) {
			if lenient && tolerated(err.Error()) {
				return
			}
			errs = append(errs, shiftLines(err.Error(), offset))
		},
	}
	conf.Check("snippet", c.fset, []*ast.File{f}, nil)
	return errs
}

var lineRef = regexp.MustCompile(`snippet\.go:(\d+):(\d+):?`)

// shiftLines réécrit « snippet.go:ligne:colonne: » en « ligne L, colonne C du bloc : » avec L relatif au bloc.
func shiftLines(msg string, offset int) string {
	return lineRef.ReplaceAllStringFunc(msg, func(s string) string {
		var line, col int
		fmt.Sscanf(s, "snippet.go:%d:%d", &line, &col)
		return fmt.Sprintf("ligne %d, colonne %d du bloc :", line-offset, col)
	})
}

// wrapFragment enveloppe des instructions dans une fonction, avec les importations qu'elles citent et les variables
// que la documentation suppose données (src, in, out). Renvoie le source et le nombre de lignes ajoutées avant le bloc.
func wrapFragment(body string) (string, int) {
	var imports []string
	for name, path := range known {
		if regexp.MustCompile(`\b` + name + `\.`).MatchString(body) {
			imports = append(imports, path)
		}
	}
	imports = append(imports, "io")
	sort.Strings(imports)
	var b strings.Builder
	b.WriteString("package p\n\nimport (\n")
	last := ""
	for _, p := range imports {
		if p != last {
			fmt.Fprintf(&b, "\t%q\n", p)
		}
		last = p
	}
	b.WriteString(")\n\nfunc _() {\n\tvar src, in io.Reader\n\tvar out io.Writer\n\t_, _, _ = src, in, out\n\t{\n")
	offset := strings.Count(b.String(), "\n")
	b.WriteString(body)
	b.WriteString("\n\t}\n}\n")
	return b.String(), offset
}

// parseDecls essaie de lire le bloc comme des déclarations de premier niveau (signatures sans corps, types).
func (c *checker) parseDecls(body string) (*ast.File, bool) {
	f, err := parser.ParseFile(token.NewFileSet(), "decls.go", "package p\n"+body, 0)
	if err != nil {
		return nil, false
	}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Body != nil {
				return nil, false
			}
		case *ast.GenDecl:
			if d.Tok == token.IMPORT {
				return nil, false
			}
		}
	}
	return f, len(f.Decls) > 0
}

// ---------------------------------------------------------------- déclarations du code

func exprs(e ast.Expr) string { return types.ExprString(e) }

// fieldList met à plat une liste de paramètres ou de champs : « a, b int » devient « a int, b int ».
func fieldList(fl *ast.FieldList) string {
	if fl == nil {
		return ""
	}
	var parts []string
	for _, f := range fl.List {
		if len(f.Names) == 0 {
			parts = append(parts, exprs(f.Type))
			continue
		}
		for _, n := range f.Names {
			parts = append(parts, n.Name+" "+exprs(f.Type))
		}
	}
	return strings.Join(parts, ", ")
}

func funcKey(fd *ast.FuncDecl) string {
	if fd.Recv != nil && len(fd.Recv.List) > 0 {
		t := exprs(fd.Recv.List[0].Type)
		return strings.TrimPrefix(t, "*") + "." + fd.Name.Name
	}
	return fd.Name.Name
}

func funcSig(fd *ast.FuncDecl) string {
	var b strings.Builder
	if fd.Recv != nil {
		fmt.Fprintf(&b, "(%s) ", fieldList(fd.Recv))
	}
	fmt.Fprintf(&b, "%s(%s)", fd.Name.Name, fieldList(fd.Type.Params))
	if fd.Type.Results != nil {
		fmt.Fprintf(&b, " (%s)", fieldList(fd.Type.Results))
	}
	return b.String()
}

// code regroupe les déclarations du paquet racine.
type code struct {
	funcs  map[string]string // clé → signature
	types  map[string]string // nom → description (champs d'une struct, ou type sous-jacent)
	values map[string]bool   // constantes et variables
}

func typeDesc(ts *ast.TypeSpec) string {
	if st, ok := ts.Type.(*ast.StructType); ok {
		return "struct{" + fieldList(st.Fields) + "}"
	}
	return exprs(ts.Type)
}

func loadCode(t *testing.T) *code {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, root, func(fi os.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }, 0)
	if err != nil {
		t.Fatal(err)
	}
	c := &code{funcs: map[string]string{}, types: map[string]string{}, values: map[string]bool{}}
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, d := range f.Decls {
				switch d := d.(type) {
				case *ast.FuncDecl:
					c.funcs[funcKey(d)] = funcSig(d)
				case *ast.GenDecl:
					for _, s := range d.Specs {
						switch s := s.(type) {
						case *ast.TypeSpec:
							c.types[s.Name.Name] = typeDesc(s)
						case *ast.ValueSpec:
							for _, n := range s.Names {
								c.values[n.Name] = true
							}
						}
					}
				}
			}
		}
	}
	if len(c.funcs) < 100 {
		t.Fatalf("seulement %d fonctions lues dans %s : le chargement du code ne marche plus ?", len(c.funcs), root)
	}
	return c
}

// checkDecls compare les déclarations montrées par un bloc à celles du code.
func (c *code) checkDecls(f *ast.File) []string {
	var errs []string
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			real, ok := c.funcs[funcKey(d)]
			switch {
			case !ok:
				errs = append(errs, fmt.Sprintf("la fonction %s n'existe pas dans le code", funcKey(d)))
			case real != funcSig(d):
				errs = append(errs, fmt.Sprintf("signature de %s :\n    doc  : %s\n    code : %s", funcKey(d), funcSig(d), real))
			}
		case *ast.GenDecl:
			for _, s := range d.Specs {
				switch s := s.(type) {
				case *ast.TypeSpec:
					real, ok := c.types[s.Name.Name]
					switch {
					case !ok:
						errs = append(errs, fmt.Sprintf("le type %s n'existe pas dans le code", s.Name.Name))
					case real != typeDesc(s):
						errs = append(errs, fmt.Sprintf("type %s :\n    doc  : %s\n    code : %s", s.Name.Name, typeDesc(s), real))
					}
				case *ast.ValueSpec:
					for _, n := range s.Names {
						if !c.values[n.Name] {
							errs = append(errs, fmt.Sprintf("la constante ou variable %s n'existe pas dans le code", n.Name))
						}
					}
				}
			}
		}
	}
	return errs
}

func TestGoSnippets(t *testing.T) {
	files := loadMarkdown(t)
	real := loadCode(t)
	c := newChecker(t)
	n := 0
	for _, f := range files {
		for _, b := range f.blocks {
			if b.lang != "go" || b.skip {
				continue
			}
			n++
			name := fmt.Sprintf("%s:%d", b.file, b.line)
			t.Run(name, func(t *testing.T) {
				body := strings.TrimSpace(b.body)
				switch {
				case strings.HasPrefix(body, "package "):
					for _, e := range c.check(b.body, false, 0) {
						t.Errorf("%s : %s", name, e)
					}
				default:
					if decls, ok := c.parseDecls(b.body); ok {
						for _, e := range real.checkDecls(decls) {
							t.Errorf("%s : %s", name, e)
						}
						return
					}
					src, offset := wrapFragment(b.body)
					for _, e := range c.check(src, true, offset) {
						t.Errorf("%s : %s", name, e)
					}
				}
			})
		}
	}
	if n < 20 {
		t.Errorf("seulement %d blocs Go vérifiés : l'extraction ne marche plus ?", n)
	}
}

// ---------------------------------------------------------------- YAML

func TestYAMLBlocks(t *testing.T) {
	n := 0
	for _, f := range loadMarkdown(t) {
		for _, b := range f.blocks {
			if b.lang != "yaml" || b.skip {
				continue
			}
			n++
			c, err := config.Parse(strings.NewReader(b.body))
			if err != nil {
				t.Errorf("%s:%d : %v", b.file, b.line, err)
				continue
			}
			if err := c.Validate(44100); err != nil {
				t.Errorf("%s:%d : %v", b.file, b.line, err)
			}
		}
	}
	if n == 0 {
		t.Error("aucun bloc YAML trouvé : l'extraction ne marche plus ?")
	}
}

// Le fichier d'exemple est valide, et chaque profil, ampli et effet qu'il nomme existe.
func TestExampleConfig(t *testing.T) {
	c, err := config.Load(filepath.Join(root, "audioprofile.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Validate(44100); err != nil {
		t.Error(err)
	}
	if names := c.ProfileNames(); len(names) <= len(audioprofile.Profiles()) {
		t.Errorf("le fichier d'exemple doit définir au moins un profil nouveau : %v", names)
	}
}
