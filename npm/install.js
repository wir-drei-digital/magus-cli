#!/usr/bin/env node
const fs = require('fs');
const path = require('path');
const https = require('https');
const { execSync } = require('child_process');

const pkg = require('./package.json');
const VERSION = pkg.version;
const REPO = 'wir-drei-digital/magus-cli';

const platform = process.platform === 'darwin' ? 'darwin'
                : process.platform === 'linux' ? 'linux'
                : null;
if (!platform) {
  console.error('Unsupported platform:', process.platform);
  process.exit(1);
}

const arch = process.arch === 'x64' ? 'amd64'
           : process.arch === 'arm64' ? 'arm64'
           : null;
if (!arch) {
  console.error('Unsupported architecture:', process.arch);
  process.exit(1);
}

const binDir = path.join(__dirname, 'bin');
fs.mkdirSync(binDir, { recursive: true });

const archive = `magus_${VERSION}_${platform}_${arch}.tar.gz`;
const url = `https://github.com/${REPO}/releases/download/v${VERSION}/${archive}`;
const tmp = path.join(binDir, archive);

function download(u, dest, cb) {
  https.get(u, (res) => {
    if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
      download(res.headers.location, dest, cb);
      return;
    }
    if (res.statusCode !== 200) {
      cb(new Error('HTTP ' + res.statusCode));
      return;
    }
    const f = fs.createWriteStream(dest);
    res.pipe(f);
    f.on('finish', () => f.close(cb));
  }).on('error', cb);
}

download(url, tmp, (err) => {
  if (err) { console.error(err); process.exit(1); }
  execSync(`tar -xzf ${tmp} -C ${binDir}`);
  fs.chmodSync(path.join(binDir, 'magus'), 0o755);
  fs.unlinkSync(tmp);
  console.log('magus installed');
});
