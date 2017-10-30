// Copyright 2017 Google Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package internal

import (
	"bufio"
	"encoding/xml"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// Project represents one of language grammars defined by a pom.xml file and a set of g4 files.
type Project struct {
	FileName string   // shortname of this grammar (ususally the directory name)
	LongName string   // long name, usually very similar to the shortname.
	Includes []string // list of g4 files
	Grammars []*Grammar

	// Test related info
	EntryPoint          string
	ExampleRoot         string
	Examples            []string
	CaseInsensitiveType string

	FoundAntlr4MavenPlugin bool // Did we actually find the right Maven plugin?
}

func (p *Project) findGrammarOfType(t string) *Grammar {
	for _, g := range p.Grammars {
		if g.Type == t {
			return g
		}
	}
	return nil
}

// ParserName returns the name of the generated Parser.
func (p *Project) ParserName() string {
	return p.grammarParserName() + "Parser"
}

// LexerName returns the name of the generated Lexer.
func (p *Project) LexerName() string {
	return p.grammarLexerName() + "Lexer"
}

// ListenerName returns the name of the of the generated Listener.
// See https://github.com/antlr/antlr4/blob/master/tool/src/org/antlr/v4/codegen/target/GoTarget.java#L168
func (p *Project) ListenerName() string {
	if g := p.findGrammarOfType("PARSER"); g != nil {
		return g.Name + "Listener"
	}

	if g := p.findGrammarOfType("COMBINED"); g != nil {
		return g.Name + "Listener"
	}

	panic(fmt.Sprintf("%q does not contain a parser", p.FileName))
}

// grammarParserName returns the name parser grammar.
func (p *Project) grammarParserName() string {
	if g := p.findGrammarOfType("PARSER"); g != nil {
		return strings.TrimSuffix(g.Name, "Parser")
	}

	if g := p.findGrammarOfType("COMBINED"); g != nil {
		return g.Name
	}

	panic(fmt.Sprintf("%q does not contain a parser", p.FileName))
}

// grammarLexerName returns the name lexer grammar.
func (p *Project) grammarLexerName() string {
	if g := p.findGrammarOfType("LEXER"); g != nil {
		return strings.TrimSuffix(g.Name, "Lexer")
	}

	if g := p.findGrammarOfType("COMBINED"); g != nil {
		return g.Name
	}

	panic(fmt.Sprintf("%q does not contain a lexer", p.FileName))
}

// GeneratedFilenames returns the list of generated files.
func (p *Project) GeneratedFilenames() []string {
	// Based on the code at:
	// https://github.com/antlr/antlr4/blob/46b3aa98cc8d8b6908c2cabb64a9587b6b973e6c/tool/src/org/antlr/v4/codegen/target/GoTarget.java#L146
	var files []string
	for _, g := range p.Grammars {
		files = append(files, g.GeneratedFilenames()...)
	}
	return files
}

// Grammar represents a Antlr G4 grammar file.
type Grammar struct {
	Name     string // name of this grammar
	Filename string
	Type     string // one of PARSER, LEXER or COMBINED // TODO(bramp): Change to enum.
}

// GeneratedFilenames returns the list of generated files.
func (g *Grammar) GeneratedFilenames() []string {
	// Based on the code at:
	// https://github.com/antlr/antlr4/blob/46b3aa98cc8d8b6908c2cabb64a9587b6b973e6c/tool/src/org/antlr/v4/codegen/target/GoTarget.java#L146
	var files []string
	switch g.Type {
	case "LEXER":
		name := strings.ToLower(strings.TrimSuffix(g.Name, "Lexer"))
		files = append(files, name+"_lexer.go")

	case "PARSER":
		name := strings.ToLower(g.Name)
		files = append(files, name+"_base_listener.go", name+"_listener.go")

		name = strings.ToLower(strings.TrimSuffix(g.Name, "Parser"))
		files = append(files, name+"_parser.go")

	case "COMBINED":
		name := strings.ToLower(g.Name)
		files = append(files, name+"_base_listener.go", name+"_listener.go")
		files = append(files, name+"_parser.go", name+"_lexer.go")

	default:
		panic(fmt.Sprintf("unknown grammar type %q", g.Type))
	}

	return files
}

func ParseG4(path string) (*Grammar, error) {
	// TODO(bramp) Use a proper antlr4 parser

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		t := ""
		if strings.HasPrefix(line, "grammar") {
			t = "COMBINED"
		} else if strings.HasPrefix(line, "lexer") {
			t = "LEXER"
		} else if strings.HasPrefix(line, "parser") {
			t = "PARSER"
		}

		if t != "" {
			if semi := strings.Index(line, ";"); semi >= 0 {
				line = line[:semi]
			}
			parts := strings.Fields(line)
			if len(parts) < 2 {
				return nil, fmt.Errorf("failed to parse grammar name: %q", line)
			}
			return &Grammar{
				Name:     parts[len(parts)-1],
				Filename: path,
				Type:     t,
			}, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("failed to find fields of interest in grammar")
}

func contains(haystack []string, needle string) bool {
	for _, straw := range haystack {
		if straw == needle {
			return true
		}
	}
	return false
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return !os.IsNotExist(err)
}

// ParsePom extracts information about the grammar in a very lazy way!
func ParsePom(path string) (*Project, error) {
	p := &Project{
		FileName: path,
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(path)

	decoder := xml.NewDecoder(file)
	for {
		t, _ := decoder.Token()
		if t == nil {
			break
		}

		switch se := t.(type) {
		case xml.StartElement:
			switch se.Name.Local {
			case "artifactId":
				var name string
				if err := decoder.DecodeElement(&name, &se); err != nil {
					return nil, err
				}
				if name == "antlr4-maven-plugin" {
					p.FoundAntlr4MavenPlugin = true
				}
			case "grammars", "include":
				var file string
				if err := decoder.DecodeElement(&file, &se); err != nil {
					return nil, err
				}
				file = filepath.Join(dir, file)

				// "Upgrade" the file to a GoTarget specific one (if it exists)
				betterfile := strings.Replace(file, ".g4", ".GoTarget.g4", -1)
				if fileExists(betterfile) {
					file = betterfile
				}

				if !fileExists(file) {
					log.Printf("missing grammar %q referenced in %q", file, path)
				} else {
					// Ignore dups
					if contains(p.Includes, file) {
						continue
					}

					p.Includes = append(p.Includes, file)

					if g, err := ParseG4(file); err != nil {
						log.Printf("failed to parse grammar %q: %s", file, err)
					} else {
						p.Grammars = append(p.Grammars, g)
					}
				}

			case "grammarName":
				var longName string
				if err := decoder.DecodeElement(&longName, &se); err != nil {
					return nil, err
				}
				p.LongName = longName

			case "entryPoint":
				var entryPoint string
				if err := decoder.DecodeElement(&entryPoint, &se); err != nil {
					return nil, err
				}
				p.EntryPoint = entryPoint

			case "exampleFiles":
				var file string
				if err := decoder.DecodeElement(&file, &se); err != nil {
					return nil, err
				}
				examples, err := filepath.Glob(filepath.Join(dir, file, "*"))
				if err != nil {
					return nil, err
				}
				p.Examples = examples
				p.ExampleRoot = strings.Repeat("../", strings.Count(dir, "/"))

			case "caseInsensitiveType":
				var caseInsensitiveType string
				if err := decoder.DecodeElement(&caseInsensitiveType, &se); err != nil {
					return nil, err
				}
				p.CaseInsensitiveType = caseInsensitiveType
			}
		}
	}

	return p, nil
}
