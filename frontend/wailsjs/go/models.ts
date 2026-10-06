export namespace main {
	
	export class Album {
	    id: number;
	    name: string;
	    count: number;
	    protected: boolean;
	    createdAt: number;
	
	    static createFrom(source: any = {}) {
	        return new Album(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.count = source["count"];
	        this.protected = source["protected"];
	        this.createdAt = source["createdAt"];
	    }
	}
	export class Media {
	    hash: string;
	    path: string;
	    name: string;
	    ext: string;
	    type: string;
	    size: number;
	    modTime: number;
	    takenAt: number;
	    width: number;
	    height: number;
	    camera: string;
	    inSafe: boolean;
	    favorite: boolean;
	    rating: number;
	    lat: number;
	    lon: number;
	    hasOCR: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Media(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hash = source["hash"];
	        this.path = source["path"];
	        this.name = source["name"];
	        this.ext = source["ext"];
	        this.type = source["type"];
	        this.size = source["size"];
	        this.modTime = source["modTime"];
	        this.takenAt = source["takenAt"];
	        this.width = source["width"];
	        this.height = source["height"];
	        this.camera = source["camera"];
	        this.inSafe = source["inSafe"];
	        this.favorite = source["favorite"];
	        this.rating = source["rating"];
	        this.lat = source["lat"];
	        this.lon = source["lon"];
	        this.hasOCR = source["hasOCR"];
	    }
	}
	export class DuplicateGroup {
	    contentHash: string;
	    count: number;
	    items: Media[];
	
	    static createFrom(source: any = {}) {
	        return new DuplicateGroup(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.contentHash = source["contentHash"];
	        this.count = source["count"];
	        this.items = this.convertValues(source["items"], Media);
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
	
	export class SafeInfo {
	    exists: boolean;
	    type: string;
	    hint: string;
	    unlocked: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SafeInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.exists = source["exists"];
	        this.type = source["type"];
	        this.hint = source["hint"];
	        this.unlocked = source["unlocked"];
	    }
	}
	export class ScanProgress {
	    scanning: boolean;
	    found: number;
	    processed: number;
	    thumbDone: number;
	    ocrDone: number;
	    ocrQueue: number;
	    thumbQ: number;
	
	    static createFrom(source: any = {}) {
	        return new ScanProgress(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.scanning = source["scanning"];
	        this.found = source["found"];
	        this.processed = source["processed"];
	        this.thumbDone = source["thumbDone"];
	        this.ocrDone = source["ocrDone"];
	        this.ocrQueue = source["ocrQueue"];
	        this.thumbQ = source["thumbQ"];
	    }
	}
	export class Section {
	    label: string;
	    from: number;
	    to: number;
	    count: number;
	
	    static createFrom(source: any = {}) {
	        return new Section(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.label = source["label"];
	        this.from = source["from"];
	        this.to = source["to"];
	        this.count = source["count"];
	    }
	}
	export class SmartAlbum {
	    id: number;
	    name: string;
	    filter: string;
	    count: number;
	
	    static createFrom(source: any = {}) {
	        return new SmartAlbum(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.filter = source["filter"];
	        this.count = source["count"];
	    }
	}

}

