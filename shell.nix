{ pkgs ? import <nixpkgs> {} }:
pkgs.mkShell {
  nativeBuildInputs = with pkgs; [ go gcc gnumake pkg-config ];
  buildInputs = with pkgs; [ sqlite ];
  CGO_ENABLED = "1";
  GOFLAGS = "-tags=sqlite_fts5";
}
