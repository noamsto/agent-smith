{
  description = "agent-smith — instruction-artifact improver (extractor, analyst, applier)";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
        version = (builtins.fromJSON (builtins.readFile ./.claude-plugin/plugin.json)).version;
        # nixpkgs' nilaway (2025-03-07) vendors an x/tools whose go/ssa panics
        # under Go 1.27. Track upstream main until nixpkgs ships a Go 1.27-ready
        # build, then drop this override. doCheck is off because upstream's own
        # test suite is OOM-killed on 7 GB CI runners.
        nilaway = pkgs.nilaway.overrideAttrs (_: {
          version = "0-unstable-2026-11-22";
          src = pkgs.fetchFromGitHub {
            owner = "uber-go";
            repo = "nilaway";
            rev = "7f797c9f2b67a10d81b207ea8c29241af77f85d7";
            hash = "sha256-H9Ex0dNdDTm5gHUeUMBVL8/02qW1IuHzTRLAgeh9/AU=";
          };
          doCheck = false;
          vendorHash = "sha256-qVmvDneq6V/q5UHZ/Cjjqd5/XPPNfvVGoxwg9nz4/Ds=";
        });
      in {
        packages.default = pkgs.buildGoModule {
          pname = "agent-smith";
          inherit version;
          ldflags = [ "-X main.version=${version}" ];
          src = ./.;
          vendorHash = null; # stdlib only
          subPackages = [ "cmd/extractor" "cmd/analyst" "cmd/applier" ];
          nativeBuildInputs = [ pkgs.makeWrapper ];
          nativeCheckInputs = [ pkgs.duckdb pkgs.git ]; # tests shell out to duckdb + git
          postInstall = ''
            for b in extractor analyst; do
              wrapProgram $out/bin/$b \
                --prefix PATH : ${pkgs.duckdb}/bin
            done
            wrapProgram $out/bin/applier \
              --prefix PATH : ${pkgs.lib.makeBinPath [ pkgs.git pkgs.gh ]}
          '';
        };

        # Static-analysis gate: golangci-lint (.golangci.yml), nilaway over the
        # module's own non-test packages (test code trips nilaway on slicing a
        # result the test has already length-checked), and the race detector
        # over the whole suite.
        checks.gate = pkgs.runCommand "agent-smith-gate" {
          src = ./.;
          nativeBuildInputs = [ pkgs.go pkgs.gcc pkgs.golangci-lint nilaway pkgs.duckdb pkgs.git ];
        } ''
          cp -r $src src && chmod -R u+w src && cd src
          export HOME=$TMPDIR GOCACHE=$TMPDIR/go-cache GOFLAGS=-mod=readonly CGO_ENABLED=1
          golangci-lint run ./...
          nilaway -test=false -include-pkgs=github.com/noamsto/agent-smith ./...
          go test -race ./...
          touch $out
        '';

        devShells.default = pkgs.mkShell {
          packages = [ pkgs.go pkgs.gopls pkgs.go-tools pkgs.golangci-lint nilaway pkgs.duckdb pkgs.jq pkgs.git pkgs.gh pkgs.goreleaser ];
        };
      })
    // {
      # Home Manager module: `programs.agent-smith.enable = true` puts the
      # extractor/analyst/applier binaries on PATH for the /agent-smith plugin.
      # The package self-wraps their runtime deps (duckdb, git, gh), so a
      # consumer only imports this module — no manual dep wiring.
      homeManagerModules.default = { config, lib, pkgs, ... }: {
        options.programs.agent-smith.enable =
          lib.mkEnableOption "the agent-smith engine (extractor/analyst/applier) on PATH";
        config = lib.mkIf config.programs.agent-smith.enable {
          home.packages = [ self.packages.${pkgs.stdenv.hostPlatform.system}.default ];
        };
      };
    };
}
