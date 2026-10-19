import type { PatchFn } from "./patches";

// The approved audition: two short rounded pulses followed by a longer one.
export const beacon:PatchFn=(ctx,out,_variant,when)=>{
 const buffer=ctx.createBuffer(1,Math.ceil(ctx.sampleRate*.65),ctx.sampleRate);
 const data=buffer.getChannelData(0);
 for(const [start,duration] of [[0,.1],[.15,.1],[.3,.3]]) {
  const begin=Math.round(start*ctx.sampleRate),length=Math.round(duration*ctx.sampleRate);
  for(let i=0;i<length;i++) {
   const t=i/ctx.sampleRate;
   const env=Math.min(1,t/.008)*Math.exp(-6*t/duration)*Math.min(1,(duration-t)/.008);
   data[begin+i]+=.4*env*(Math.sin(2*Math.PI*740*t)+.15*Math.sin(2*Math.PI*1480*t));
  }
 }
 const source=ctx.createBufferSource();source.buffer=buffer;source.connect(out);source.start(when);
};
