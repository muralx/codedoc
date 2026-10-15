// Adversarial probes of local variable scoping: each call carries the
// edge it must yield, as the name called, the tier and the targets.
import { Alpha, Beta, Beta as Renamed } from "./kit";

export interface Holder { alpha: Alpha }

export const store = new Alpha();

// 1 nested block shadow
export function blockShadow(): void {
  const x = new Alpha();
  {
    const x = new Beta();
    x.run();            // => run import Beta.run
  }
  x.run();              // => run import Alpha.run
}

// 2 arrow single param, no parens
export function arrowBare(items: Beta[]): void {
  const x = new Alpha();
  items.forEach(x => x.run());   // => run ambiguous Alpha.run|Beta.run
  x.close();                     // => close import Alpha.close
}

// 3 destructured arrow param
export function destructured(list: { x: Beta }[]): void {
  const x = new Alpha();
  list.forEach(({ x }) => x.run());  // => run ambiguous Alpha.run|Beta.run
}

// 4 typed closure param does not leak after the closure
export function closureLeak(list: Beta[]): void {
  const x = new Alpha();
  list.forEach((x: Beta) => x.run());   // => run import Beta.run
  x.close();                             // => close import Alpha.close
}

// 5 destructuring declaration in nested block
export function destructDecl(h: { x: Beta }): void {
  const x = new Alpha();
  if (h) {
    const { x } = h;
    x.run();           // => run ambiguous Alpha.run|Beta.run
  }
}

// 6 annotation vs new
export function annot(): void {
  const x: Alpha = new Beta() as unknown as Alpha;
  x.run();             // => run import Alpha.run
  let y: Alpha;
  y = new Beta() as never;
  y!.run();            // => run ambiguous Alpha.run|Beta.run
}

// 7 reassign with let
export function reassign(flag: boolean): void {
  let x = new Alpha();
  x.run();              // => run import Alpha.run
  let z: Alpha | Beta = new Alpha();
  z = new Beta();
  z.run();              // => run ambiguous Alpha.run|Beta.run
}

// 8 for-of
export function loops(bs: Beta[]): void {
  const x = new Alpha();
  for (const x of bs) { x.run(); }   // => run import Beta.run
  x.close();                         // => close import Alpha.close
}

// 9 nested named function
export function nested(): void {
  const x = new Alpha();
  function inner(x: Beta) { x.run(); }  // => run import Beta.run
  inner(new Beta());
  x.run();   // => run import Alpha.run
}

// 10 use before the declaration, inside a closure
export function later(): void {
  const cb = () => { w.run(); };   // => run ambiguous Alpha.run|Beta.run
  const w = new Alpha();
  cb();
}

// 11 class property
export class K {
  private a = new Alpha();
  private b: Beta;
  constructor() { this.b = new Beta(); }
  go(a: Beta): void {
    a.run();          // => run import Beta.run
    this.a.run();     // => run import Alpha.run
    this.b.run();     // => run import Beta.run
  }
  other(): void {
    const self = { a: new Beta() };
    self.a.run();     // => run ambiguous Alpha.run|Beta.run
  }
}

// 12 catch variable hides
export function caught(): void {
  const x = new Alpha();
  try { x.run(); } catch (x) { x.close(); }  // => run import Alpha.run
}

// 13 a variable of unknown type never yields a confident guess
export function blind(items: () => unknown[]): void {
  for (const it of items()) { it.only(); }  // => only ambiguous Beta.only
}

// 14 module-level variables and import aliases
export function moduleLevel(r: Renamed): void {
  store.run();        // => run import Alpha.run
  below.close();      // => close import Beta.close
  r.only();           // => only import Beta.only
  const made = new Renamed();
  made.run();         // => run import Beta.run
}

const below = new Beta();
