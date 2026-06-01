{
  description = "agent-smith — Track A corpus-mining extractor";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let pkgs = import nixpkgs { inherit system; };
      in {
        packages.default = pkgs.buildGoModule {
          pname = "agent-smith";
          version = "0.1.0";
          src = ./.;
          vendorHash = null; # stdlib only
          subPackages = [ "cmd/extractor" ];
          nativeBuildInputs = [ pkgs.makeWrapper ];
          nativeCheckInputs = [ pkgs.duckdb ]; # tests shell out to duckdb
          postInstall = ''
            wrapProgram $out/bin/extractor \
              --prefix PATH : ${pkgs.duckdb}/bin
          '';
        };

        devShells.default = pkgs.mkShell {
          packages = [ pkgs.go pkgs.gopls pkgs.go-tools pkgs.duckdb pkgs.jq ];
        };
      });
}
