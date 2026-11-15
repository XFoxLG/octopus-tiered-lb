"""Exercise the release command with tool stubs in a temporary Git checkout."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).with_name('build.sh')
BASH = shutil.which('bash')
if BASH is None and os.name == 'nt':
    candidate = Path('C:/Program Files/Git/bin/bash.exe')
    if candidate.is_file():
        BASH = str(candidate)


@unittest.skipUnless(BASH and shutil.which('git'), 'Bash and Git are required')
class ReleaseSourceTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root/'scripts').mkdir()
        (self.root/'web').mkdir()
        (self.root/'internal/price').mkdir(parents=True)
        (self.root/'static').mkdir()
        (self.root/'tools').mkdir()
        shutil.copyfile(SCRIPT, self.root/'scripts/build.sh')
        (self.root/'.gitignore').write_text('build/\nweb/out/\nstatic/out/\ncommands.log\n', encoding='utf-8')
        (self.root/'README.md').write_text('fixture\n', encoding='utf-8')
        (self.root/'LICENSE').write_text('fixture\n', encoding='utf-8')
        (self.root/'static/README.md').write_text('embed parent\n', encoding='utf-8')
        (self.root/'go.mod').write_text('module fixture\ngo 1.27.3\n', encoding='utf-8')
        (self.root/'internal/price/presets.go').write_text('package price\n', encoding='utf-8')
        (self.root/'web/pnpm-lock.yaml').write_text('lockfileVersion: 9.0\n', encoding='utf-8')
        self.stub('go', '''
printf 'go %s\\n' "$*" >> "$ROOT/commands.log"
case "$1" in
  version) echo 'go version go1.27.3 linux/amd64' ;;
  mod)
    if [ "$2" != download ]; then echo 'unexpected module mutation' >&2; exit 41; fi
    if [ "${MUTATE_SOURCE:-}" = modules ]; then echo '// changed' >> "$ROOT/go.mod"; fi
    ;;
  build)
    if [ "${FAIL_PLATFORM:-}" = "$GOOS/$GOARCH" ]; then exit 42; fi
    shift
    while [ "$#" -gt 0 ]; do
      if [ "$1" = -o ]; then shift; mkdir -p "$(dirname "$1")"; echo fixture > "$1"; break; fi
      shift
    done
    ;;
  *) exit 43 ;;
esac
''')
        self.stub('node', "echo 'v22.0.0'\n")
        self.stub('pnpm', '''
printf 'pnpm %s\\n' "$*" >> "$ROOT/commands.log"
case "$1" in
  --version) echo '10.22.0' ;;
  install)
    if [ "$2" != --frozen-lockfile ]; then echo 'unlocked install' >&2; exit 44; fi
    ;;
  run)
    mkdir -p out; echo fixture > out/index.html
    if [ "${MUTATE_SOURCE:-}" = frontend ]; then echo changed >> pnpm-lock.yaml; fi
    ;;
  *) exit 45 ;;
esac
''')
        self.stub('python3', '''
printf 'python3 %s\\n' "$*" >> "$ROOT/commands.log"
if [ "$1" = --version ]; then echo 'Python 3.13.0'; exit; fi
echo '// unreviewed price data' >> "$ROOT/internal/price/presets.go"
exit 46
''')
        self.stub('curl', 'exit 47\n')
        self.stub('zip', 'if [ "${FAIL_ARCHIVE:-}" = "$2" ]; then exit 48; fi\ntouch "$2"\n')
        self.env = {**os.environ,'GIT_CONFIG_NOSYSTEM':'1','ROOT':self.root.as_posix()}
        if os.name == 'nt':
            base = '/c/Program Files/Git/usr/bin:/c/Program Files/Git/mingw64/bin'
            self.env['PATH'] = self.root.as_posix()+'/tools:'+base
        else:
            self.env['PATH'] = str(self.root/'tools')+os.pathsep+os.environ['PATH']
        for args in [('init',),('config','user.name','Release fixture'),('config','user.email','fixture@example.invalid'),('add','.'),('commit','-m','release fixture'),('tag','v1.0.0')]:
            subprocess.run(['git',*args],cwd=self.root,env=self.env,capture_output=True,check=True)
        self.sha = subprocess.check_output(['git','rev-parse','HEAD'],cwd=self.root,env=self.env,text=True).strip()

    def stub(self, name, body):
        path = self.root/'tools'/name
        path.write_text('#!/bin/bash\nset -e\n'+body,encoding='utf-8',newline='\n')
        path.chmod(0o755)

    def release(self, **settings):
        return subprocess.run([BASH,'scripts/build.sh','release'],cwd=self.root,env={**self.env,**settings},capture_output=True,text=True,encoding='utf-8')

    def test_release_keeps_committed_source_and_builds_all_platforms(self):
        result = self.release()
        self.assertEqual(result.returncode,0,result.stdout+result.stderr)
        commands = (self.root/'commands.log').read_text()
        self.assertIn('go mod download',commands)
        self.assertIn('pnpm install --frozen-lockfile',commands)
        self.assertNotIn('updatePrice.py',commands)
        self.assertNotIn('go mod tidy',commands)
        self.assertEqual(commands.count('go build -mod=readonly '),8)
        self.assertIn(self.sha,commands)
        self.assertEqual(len(list((self.root/'build/archives').glob('*.zip'))),8)
        changed = subprocess.check_output(['git','status','--porcelain'],cwd=self.root,env=self.env,text=True)
        self.assertEqual(changed,'')

    def test_dirty_checkout_rejected_before_build(self):
        (self.root/'README.md').write_text('edited\n',encoding='utf-8')
        result = self.release()
        self.assertNotEqual(result.returncode,0)
        self.assertIn('Release source differs',result.stderr)
        self.assertFalse((self.root/'commands.log').exists())

    def test_dependency_download_cannot_change_source(self):
        result = self.release(MUTATE_SOURCE='modules')
        self.assertNotEqual(result.returncode,0)
        self.assertIn('go.mod',result.stderr)
        self.assertNotIn('go build ',(self.root/'commands.log').read_text())

    def test_frontend_build_cannot_change_lockfile(self):
        result = self.release(MUTATE_SOURCE='frontend')
        self.assertNotEqual(result.returncode,0)
        self.assertIn('pnpm-lock.yaml',result.stderr)
        self.assertNotIn('go build ',(self.root/'commands.log').read_text())

    def test_failed_platform_does_not_publish_partial_archives(self):
        result = self.release(FAIL_PLATFORM='linux/arm64')
        self.assertNotEqual(result.returncode,0)
        self.assertIn('Failed to build Linux arm64',result.stderr)
        self.assertEqual(len(list((self.root/'build/archives').glob('*.zip'))),0)

    def test_archive_failure_stops_release(self):
        result = self.release(FAIL_ARCHIVE='octopus-linux-arm64.zip')
        self.assertNotEqual(result.returncode,0)
        self.assertIn('Failed to create archive: octopus-linux-arm64.zip',result.stderr)


if __name__ == '__main__':
    unittest.main()
