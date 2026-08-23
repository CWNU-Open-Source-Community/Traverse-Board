use std::env;
use std::fs;
use std::io::ErrorKind;
use std::path::Path;

fn main() {
    for name in ["HOME", "SSH_AUTH_SOCK", "DOCKER_HOST", "GIT_ASKPASS", "AWS_ACCESS_KEY_ID"] {
        assert!(env::var_os(name).is_none(), "host credential environment leaked through {name}");
    }
    for name in ["/var/run/docker.sock", "/root/.ssh", "/host"] {
        match Path::new(name).try_exists() {
            Ok(false) => {}
            Ok(true) => panic!("undeclared host path is visible: {name}"),
            Err(error) if matches!(error.kind(), ErrorKind::NotFound | ErrorKind::PermissionDenied) => {}
            Err(error) => panic!("undeclared host path check failed for {name}: {error}"),
        }
    }
    fs::write("rust-output.txt", b"rust offline fixture\n").expect("write Workspace output");
}
