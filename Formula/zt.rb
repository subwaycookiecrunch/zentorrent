class Zt < Formula
  desc "Lightweight torrent streaming CLI"
  homepage "https://github.com/subwaycookiecrunch/zentorrent"
  version "2.0.0"

  on_macos do
    on_arm do
      url "https://github.com/subwaycookiecrunch/zentorrent/releases/download/v2.0.0/zt-macos-arm64"
      sha256 "FILL_AFTER_RELEASE"
    end
    on_intel do
      url "https://github.com/subwaycookiecrunch/zentorrent/releases/download/v2.0.0/zt-macos-amd64"
      sha256 "FILL_AFTER_RELEASE"
    end
  end

  def install
    bin.install Dir["zt-macos-*"].first => "zt"
  end
end
