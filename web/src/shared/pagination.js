export const listPageSizes=Object.freeze([20,50,100])
export function paginationState(total,page=1,size=20){
 const normalized=listPageSizes.includes(size)?size:20
 const pages=Math.max(1,Math.ceil(Math.max(0,Number(total)||0)/normalized))
 return {size:normalized,pages,page:Math.min(pages,Math.max(1,Number(page)||1))}
}
