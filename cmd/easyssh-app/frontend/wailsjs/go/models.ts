export namespace app {
	
	export class DeployEditView {
	    type: string;
	    reload_cmd?: string;
	    test_cmd?: string;
	    cert_path?: string;
	    key_path?: string;
	    host?: string;
	    host_ref?: string;
	    port?: number;
	    user?: string;
	    ssh_key?: string;
	    remote_path?: string;
	    cert_filename?: string;
	    key_filename?: string;
	    dir?: string;
	    url?: string;
	
	    static createFrom(source: any = {}) {
	        return new DeployEditView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.reload_cmd = source["reload_cmd"];
	        this.test_cmd = source["test_cmd"];
	        this.cert_path = source["cert_path"];
	        this.key_path = source["key_path"];
	        this.host = source["host"];
	        this.host_ref = source["host_ref"];
	        this.port = source["port"];
	        this.user = source["user"];
	        this.ssh_key = source["ssh_key"];
	        this.remote_path = source["remote_path"];
	        this.cert_filename = source["cert_filename"];
	        this.key_filename = source["key_filename"];
	        this.dir = source["dir"];
	        this.url = source["url"];
	    }
	}
	export class CertEditView {
	    name: string;
	    domains: string[];
	    challenge: string;
	    dns_provider: string;
	    dns_opts: Record<string, string>;
	    storage_dir: string;
	    deploys: DeployEditView[];
	
	    static createFrom(source: any = {}) {
	        return new CertEditView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.domains = source["domains"];
	        this.challenge = source["challenge"];
	        this.dns_provider = source["dns_provider"];
	        this.dns_opts = source["dns_opts"];
	        this.storage_dir = source["storage_dir"];
	        this.deploys = this.convertValues(source["deploys"], DeployEditView);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class CertView {
	    name: string;
	    domains: string[];
	    not_after: string;
	    remain_days: number;
	    status: string;
	    fingerprint: string;
	    issuer: string;
	    deployed: boolean;
	    last_error?: string;
	
	    static createFrom(source: any = {}) {
	        return new CertView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.domains = source["domains"];
	        this.not_after = source["not_after"];
	        this.remain_days = source["remain_days"];
	        this.status = source["status"];
	        this.fingerprint = source["fingerprint"];
	        this.issuer = source["issuer"];
	        this.deployed = source["deployed"];
	        this.last_error = source["last_error"];
	    }
	}
	export class HostEditView {
	    name: string;
	    host: string;
	    port?: number;
	    user: string;
	    key?: string;
	    remote_path?: string;
	    reload_cmd?: string;
	    cert_filename?: string;
	    key_filename?: string;
	
	    static createFrom(source: any = {}) {
	        return new HostEditView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.host = source["host"];
	        this.port = source["port"];
	        this.user = source["user"];
	        this.key = source["key"];
	        this.remote_path = source["remote_path"];
	        this.reload_cmd = source["reload_cmd"];
	        this.cert_filename = source["cert_filename"];
	        this.key_filename = source["key_filename"];
	    }
	}
	export class ConfigView {
	    ca_server: string;
	    ca_email: string;
	    account_key: string;
	    check_interval: string;
	    renew_before: string;
	    retry_backoff: string[];
	    webhook: string;
	    smtp_host?: string;
	    smtp_port?: number;
	    smtp_user?: string;
	    smtp_pass?: string;
	    smtp_to?: string[];
	    notify_expiring: boolean;
	    notify_success: boolean;
	    autostart: boolean;
	    hosts?: HostEditView[];
	    certificates: CertEditView[];
	
	    static createFrom(source: any = {}) {
	        return new ConfigView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ca_server = source["ca_server"];
	        this.ca_email = source["ca_email"];
	        this.account_key = source["account_key"];
	        this.check_interval = source["check_interval"];
	        this.renew_before = source["renew_before"];
	        this.retry_backoff = source["retry_backoff"];
	        this.webhook = source["webhook"];
	        this.smtp_host = source["smtp_host"];
	        this.smtp_port = source["smtp_port"];
	        this.smtp_user = source["smtp_user"];
	        this.smtp_pass = source["smtp_pass"];
	        this.smtp_to = source["smtp_to"];
	        this.notify_expiring = source["notify_expiring"];
	        this.notify_success = source["notify_success"];
	        this.autostart = source["autostart"];
	        this.hosts = this.convertValues(source["hosts"], HostEditView);
	        this.certificates = this.convertValues(source["certificates"], CertEditView);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class ExportRequest {
	    out_path: string;
	    scope: bundle.Scope;
	    password: string;
	
	    static createFrom(source: any = {}) {
	        return new ExportRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.out_path = source["out_path"];
	        this.scope = this.convertValues(source["scope"], bundle.Scope);
	        this.password = source["password"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ExportResult {
	    out_path: string;
	    cert_names: string[];
	    env_secrets: string[];
	    ssh_key_n: number;
	    warning: string;
	
	    static createFrom(source: any = {}) {
	        return new ExportResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.out_path = source["out_path"];
	        this.cert_names = source["cert_names"];
	        this.env_secrets = source["env_secrets"];
	        this.ssh_key_n = source["ssh_key_n"];
	        this.warning = source["warning"];
	    }
	}
	
	export class ImportPreview {
	    exported_at: string;
	    has_secrets: boolean;
	    has_ssh_keys: boolean;
	    has_certs: boolean;
	    cert_names: string[];
	    env_secrets: string[];
	    ssh_key_paths: string[];
	    needs_pass: boolean;
	    warning: string;
	
	    static createFrom(source: any = {}) {
	        return new ImportPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.exported_at = source["exported_at"];
	        this.has_secrets = source["has_secrets"];
	        this.has_ssh_keys = source["has_ssh_keys"];
	        this.has_certs = source["has_certs"];
	        this.cert_names = source["cert_names"];
	        this.env_secrets = source["env_secrets"];
	        this.ssh_key_paths = source["ssh_key_paths"];
	        this.needs_pass = source["needs_pass"];
	        this.warning = source["warning"];
	    }
	}
	export class ImportPreviewRequest {
	    file_path: string;
	    password: string;
	
	    static createFrom(source: any = {}) {
	        return new ImportPreviewRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.file_path = source["file_path"];
	        this.password = source["password"];
	    }
	}
	export class ImportRequest {
	    file_path: string;
	    password: string;
	    conflict: string;
	
	    static createFrom(source: any = {}) {
	        return new ImportRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.file_path = source["file_path"];
	        this.password = source["password"];
	        this.conflict = source["conflict"];
	    }
	}
	export class Overview {
	    total: number;
	    healthy: number;
	    expiring: number;
	    failed: number;
	    not_issued: number;
	    schedule: string;
	    config_path: string;
	    last_run: string;
	    last_error?: string;
	    missing_env?: string[];
	    ca: string;
	
	    static createFrom(source: any = {}) {
	        return new Overview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.total = source["total"];
	        this.healthy = source["healthy"];
	        this.expiring = source["expiring"];
	        this.failed = source["failed"];
	        this.not_issued = source["not_issued"];
	        this.schedule = source["schedule"];
	        this.config_path = source["config_path"];
	        this.last_run = source["last_run"];
	        this.last_error = source["last_error"];
	        this.missing_env = source["missing_env"];
	        this.ca = source["ca"];
	    }
	}
	export class SSHTestParams {
	    host_ref?: string;
	    host?: string;
	    port?: number;
	    user?: string;
	    key?: string;
	    known_hosts?: string;
	
	    static createFrom(source: any = {}) {
	        return new SSHTestParams(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.host_ref = source["host_ref"];
	        this.host = source["host"];
	        this.port = source["port"];
	        this.user = source["user"];
	        this.key = source["key"];
	        this.known_hosts = source["known_hosts"];
	    }
	}

}

export namespace bundle {
	
	export class Scope {
	    config: boolean;
	    secrets: boolean;
	    certs: boolean;
	    ssh_keys: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Scope(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.config = source["config"];
	        this.secrets = source["secrets"];
	        this.certs = source["certs"];
	        this.ssh_keys = source["ssh_keys"];
	    }
	}

}

export namespace frontend {
	
	export class FileFilter {
	    DisplayName: string;
	    Pattern: string;
	
	    static createFrom(source: any = {}) {
	        return new FileFilter(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.DisplayName = source["DisplayName"];
	        this.Pattern = source["Pattern"];
	    }
	}
	export class OpenDialogOptions {
	    DefaultDirectory: string;
	    DefaultFilename: string;
	    Title: string;
	    Filters: FileFilter[];
	    ShowHiddenFiles: boolean;
	    CanCreateDirectories: boolean;
	    ResolvesAliases: boolean;
	    TreatPackagesAsDirectories: boolean;
	
	    static createFrom(source: any = {}) {
	        return new OpenDialogOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.DefaultDirectory = source["DefaultDirectory"];
	        this.DefaultFilename = source["DefaultFilename"];
	        this.Title = source["Title"];
	        this.Filters = this.convertValues(source["Filters"], FileFilter);
	        this.ShowHiddenFiles = source["ShowHiddenFiles"];
	        this.CanCreateDirectories = source["CanCreateDirectories"];
	        this.ResolvesAliases = source["ResolvesAliases"];
	        this.TreatPackagesAsDirectories = source["TreatPackagesAsDirectories"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SaveDialogOptions {
	    DefaultDirectory: string;
	    DefaultFilename: string;
	    Title: string;
	    Filters: FileFilter[];
	    ShowHiddenFiles: boolean;
	    CanCreateDirectories: boolean;
	    TreatPackagesAsDirectories: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SaveDialogOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.DefaultDirectory = source["DefaultDirectory"];
	        this.DefaultFilename = source["DefaultFilename"];
	        this.Title = source["Title"];
	        this.Filters = this.convertValues(source["Filters"], FileFilter);
	        this.ShowHiddenFiles = source["ShowHiddenFiles"];
	        this.CanCreateDirectories = source["CanCreateDirectories"];
	        this.TreatPackagesAsDirectories = source["TreatPackagesAsDirectories"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace logring {
	
	export class Entry {
	    // Go type: time
	    time: any;
	    msg: string;
	
	    static createFrom(source: any = {}) {
	        return new Entry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.time = this.convertValues(source["time"], null);
	        this.msg = source["msg"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

