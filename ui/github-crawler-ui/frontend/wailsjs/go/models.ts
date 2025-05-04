export namespace api {
	
	export class CrawlStats {
	    version: string;
	    isRunning: boolean;
	    // Go type: time
	    startTime: any;
	    duration: string;
	    reposCrawled: number;
	    releasesCrawled: number;
	    commitsCrawled: number;
	    lastError: string;
	
	    static createFrom(source: any = {}) {
	        return new CrawlStats(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.isRunning = source["isRunning"];
	        this.startTime = this.convertValues(source["startTime"], null);
	        this.duration = source["duration"];
	        this.reposCrawled = source["reposCrawled"];
	        this.releasesCrawled = source["releasesCrawled"];
	        this.commitsCrawled = source["commitsCrawled"];
	        this.lastError = source["lastError"];
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

