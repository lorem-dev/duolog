package main

import (
	"os"
	"path/filepath"
	"testing"
)

const mit = `Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:
The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.
THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT.`

const bsd = `Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:
1. Redistributions of source code must retain the above copyright notice,
this list of conditions and the following disclaimer.
2. Redistributions in binary form must reproduce the above copyright notice,
this list of conditions and the following disclaimer in the documentation
and/or other materials provided with the distribution.
THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
AND ANY EXPRESS OR IMPLIED WARRANTIES ARE DISCLAIMED.`

const endorsement = `Neither the name of the copyright holder nor the names of its
contributors may be used to endorse or promote products derived from this
software without specific prior written permission.`

const apache = `Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
http://www.apache.org/licenses/LICENSE-2.0`

func TestClassifyLicense(t *testing.T) {
	for _, tc := range []struct{ name, text, want string }{
		{"MIT", mit, "MIT"},
		{"BSD two clause", bsd, "BSD-2-Clause"},
		{"BSD three clause", bsd + endorsement, "BSD-3-Clause"},
		{"Apache", apache, "Apache-2.0"},
		{"YAML mixed license", mit + apache, "MIT AND Apache-2.0"},
		{"unknown", "All rights reserved. No copying allowed.", ""},
		{"copyleft", "GNU General Public License version 3", ""},
		{"copyleft with MIT text", mit + "GNU Affero General Public License", ""},
		{"MPL", "Mozilla Public License Version 2.0", ""},
		{"BSD advertising", bsd + endorsement + "All advertising materials must display an acknowledgement.", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := classifyLicense(tc.text)
			if tc.want == "" {
				if err == nil {
					t.Fatalf("accepted unsupported license as %s", got)
				}
			} else if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestModuleLicenseFailsClosed(t *testing.T) {
	dir := t.TempDir()
	if _, err := moduleLicense(dir); err == nil {
		t.Fatal("accepted module without a license")
	}
	if err := os.WriteFile(filepath.Join(dir, "LICENSE"), []byte(mit), 0644); err != nil {
		t.Fatal(err)
	}
	if got, err := moduleLicense(dir); err != nil || got != "MIT" {
		t.Fatalf("got %q, %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "LICENSE.extra"), []byte("Unrecognized terms"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := moduleLicense(dir); err == nil {
		t.Fatal("ignored unknown additional license")
	}
}
