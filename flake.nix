{
  description = "Sagittarius A* (aig) — terminal AI agent development environment";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };

        # The Go version must match go.mod (1.22+).
        goEnv = pkgs.go_1_24;

        aig = pkgs.buildGoModule {
          pname = "aig";
          version = "0.1.0";
          src = ./.;

          # Update this hash after running `nix build` for the first time.
          # Run: nix build 2>&1 | grep "got:" | awk '{print $2}'
          vendorHash = pkgs.lib.fakeHash;

          subPackages = [ "cmd/aig" ];

          meta = with pkgs.lib; {
            description = "Cross-platform terminal AI agent supporting Gemini and DeepSeek";
            homepage = "https://github.com/rhea/sagittarius-astar";
            license = licenses.mit;
            mainProgram = "aig";
          };
        };
      in
      {
        # `nix build` produces the aig binary.
        packages = {
          default = aig;
          aig = aig;
        };

        # `nix develop` enters the development shell.
        devShells.default = pkgs.mkShell {
          buildInputs = [
            goEnv
            pkgs.gopls          # Go language server
            pkgs.gotools         # goimports, etc.
            pkgs.golangci-lint   # linting suite
            pkgs.git
            pkgs.gnumake
          ];

          shellHook = ''
            echo ""
            echo "  ✦ Sagittarius A* dev shell"
            echo "  Go $(go version | awk '{print $3}')"
            echo ""
            echo "  Commands:"
            echo "    make build         build for current OS"
            echo "    make build-all     cross-compile linux + windows"
            echo "    make test          run tests"
            echo "    make run           build and run"
            echo ""
          '';

          # Ensure CGO is disabled for clean cross-compilation.
          CGO_ENABLED = "0";
        };
      });
}
