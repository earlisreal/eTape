import { formatPrice,quoteDecimals } from "../../render/format";

export function snapAlertPrice(price:number):number {return Number(price.toFixed(quoteDecimals(price)));}
export function alertPriceText(price:number):string {return formatPrice(price,quoteDecimals(price));}
